package cids_polling

import (
	"context"
	// "log"
	"log/slog"
	"math"
	"time"
)

type Message uint32

type Config struct {
	BatchSize int     // N
	Lambda    float64 // Forgetting factor for RLS
	InitialP  float64 // Initial covariance (delta * I)
	//CusumAllowance float64
	CusumThreshold float64

	EnableGlobalAlarmForwarding bool // Enable global alarm forwarding for each message profile
}

func DefaultConfig() Config {
	return Config{
		BatchSize:                   20,
		Lambda:                      0.9995,
		InitialP:                    1e6, // Large value for initial uncertainty
		CusumThreshold:              5,   // 5 as written in the paper
		EnableGlobalAlarmForwarding: false,
	}
}

type History struct {
	timeStamp time.Time

	LastMsgA0 time.Time
	MuT       float64
	OAcc      float64 // O_acc[k]
	S         float64 // S[k]
	P         float64 // P[k]

	// CUSUM state
	CusumH float64
	CusumL float64
	LastEK float64 // Identification error e[k]
}

type Profile struct {
	ID     Message
	Config Config
	ch     chan Message
	alert  chan bool

	// Buffering
	Buffer    []time.Time
	LastMsgA0 time.Time // a_0 from the previous batch
	IsInit    bool      // True when k=0 is done

	// RLS state
	MuT       float64 // \mu_T[k-1] in seconds
	OAcc      float64 // O_acc[k]
	S         float64 // S[k]
	P         float64 // P[k]
	StartTime time.Time

	// CUSUM state
	CusumH float64
	CusumL float64
	LastEK float64 // Identification error e[k]

	messageCount uint64
	meanE        float64
	varianceE    float64
	M2           float64
	K            float64

	history []History
}

func (p *Profile) processBatch() {
	N := p.Config.BatchSize

	// Calculate \mu_T[k] (Algorithm Line 22)
	// T_1 = a_1 - a_0 (where a_0 is from previous batch)
	var sumT float64
	sumT += float64(p.Buffer[0].Sub(p.LastMsgA0).Seconds())
	for i := 1; i < N; i++ {
		sumT += float64(p.Buffer[i].Sub(p.Buffer[i-1]).Seconds())
	}
	muT_k := sumT / float64(N)

	// Calculate O[k] (Algorithm Line 23)
	// O[k] = 1/(N-1) * sum_{i=2}^N (a_i - (a_1 + (i-1)*\mu_T[k-1]))
	// Note: 1-indexed in algorithm maps to 0-indexed in Go buffer.
	// a_1 is p.Buffer[0]. a_i is p.Buffer[i-1].
	var O_k float64
	a_1 := p.Buffer[0]
	for i := 1; i < N; i++ {
		a_i := p.Buffer[i]
		expected := a_1.Add(time.Duration(float64(i) * p.MuT * float64(time.Second)))

		diff := a_i.Sub(expected).Seconds()
		O_k += diff
	}
	if N > 1 {
		O_k = O_k / float64(N-1)
	}

	// O_acc[k] (Algorithm Line 24)
	p.OAcc = p.OAcc + math.Abs(O_k)

	// Calculate t[k] - elapsed nominal time in seconds
	t_k := p.Buffer[N-1].Sub(p.StartTime).Seconds()

	// e[k] (Algorithm Line 25)
	e_k := p.OAcc - p.S*t_k
	p.LastEK = e_k

	// SKEWUPDATE (Algorithm Lines 2-6)
	lambdaInv := 1.0 / p.Config.Lambda

	// G[k] = (lambda^-1 * P[k-1] * t[k]) / (1 + lambda^-1 * t[k]^2 * P[k-1])
	G_k := (lambdaInv * p.P * t_k) / (1.0 + lambdaInv*t_k*t_k*p.P)

	// P[k] = lambda^-1 * (P[k-1] - G[k]*t[k]*P[k-1])
	p.P = lambdaInv * (p.P - G_k*t_k*p.P)

	// S[k] = S[k-1] + G[k]*e[k]
	p.S = p.S + G_k*e_k

	// Save \mu_T[k] for next step (Algorithm end for)
	p.MuT = muT_k
	// Save state for next step
	p.LastMsgA0 = p.Buffer[N-1]

	// Section 4.2 - Detection
	// Update mean and variance of identification error e[k]
	p.messageCount += 1

	deltaE := e_k - p.meanE
	newMeanE := p.meanE + deltaE/float64(p.messageCount)
	newM2 := p.M2 + deltaE*(newMeanE-e_k)
	newVarianceE := newM2 / float64(p.messageCount-1)

	// Update the CUSUM as written in (4)
	if p.IsInit || math.Abs((e_k-newMeanE)/math.Sqrt(newVarianceE)) < 3 {
		p.meanE = newMeanE
		p.M2 = newM2
		p.varianceE = newVarianceE
	}

	update := (e_k - p.meanE) / (math.Sqrt(p.varianceE) - p.K)
	p.CusumH = math.Max(0, p.CusumH+update)
	p.CusumL = math.Max(0, p.CusumL-update)

	// Saves the current values to plot later
	p.history = append(p.history, History{
		timeStamp: time.Now(),
		LastMsgA0: p.LastMsgA0,
		MuT:       p.MuT,
		OAcc:      p.OAcc,
		S:         p.S,
		P:         p.P,
		CusumH:    p.CusumH,
		CusumL:    p.CusumL,
		LastEK:    p.LastEK,
	})

	newAlarm := (p.CusumH > p.Config.CusumThreshold || p.CusumL > p.Config.CusumThreshold)

	if newAlarm {
		slog.Info("Alarm triggered")
	}

	// Send alert to global alarm with select for non-blocking operation
	select {
	// Flush the previous alarm
	case currentAlarm := <-p.alert:
		// Send the new alarm without overriding a previous alert (if there was one)
		p.alert <- currentAlarm || newAlarm
	default:
		// Send directly the the alart if there was no alert in queue
		p.alert <- newAlarm
	}
}

func (p *Profile) Listen(ctx context.Context) {
	for {
		// In the init phase, we just wait for messages, collect until buffer is full, then run processBatch
		if p.IsInit {
			select {
			case <-ctx.Done():
				return
			case id := <-p.ch:
				if id == p.ID {
					p.Buffer = append(p.Buffer, time.Now())
					if len(p.Buffer) == p.Config.BatchSize+1 {
						var sumT float64
						for i := 1; i <= p.Config.BatchSize; i++ {
							sumT += p.Buffer[i].Sub(p.Buffer[i-1]).Seconds()
						}
						p.MuT = sumT / float64(p.Config.BatchSize)
						p.LastMsgA0 = p.Buffer[p.Config.BatchSize]
						p.StartTime = p.Buffer[p.Config.BatchSize]
						p.Buffer = p.Buffer[:0]
						p.meanE = p.LastEK
						p.messageCount = 1
						p.IsInit = false
					}
				}
			}
		} else {
			// After the init phase, we know the interval so we can start accounting for suspension attack
			select {
			// Gracefully stop the goroutine
			case <-ctx.Done():
				return
			// Message received
			case id := <-p.ch:
				if id == p.ID {
					p.Buffer = append(p.Buffer, time.Now())
					if len(p.Buffer) == p.Config.BatchSize {
						p.processBatch()
						p.Buffer = p.Buffer[:0]
					}
				}
			// Flag suspension time after 2x the expected batch duration.
			case <-time.After(time.Duration(p.MuT * float64(p.Config.BatchSize) * 2.0 * float64(time.Second))):
				slog.Info("Suspension")
				currentTime := time.Now()
				p.Buffer = append(p.Buffer, currentTime)
				if len(p.Buffer) == p.Config.BatchSize {
					p.processBatch()
					p.Buffer = p.Buffer[:0]
				}
			}
		}
	}
}

func (p *Profile) forwardToGlobalAlarm(context context.Context, globalAlarm chan bool) {
	for {
		select {
		case <-context.Done():
			return
		case alarm := <-p.alert:
			globalAlarm <- alarm
		}
	}
}

type Detector struct {
	context     context.Context
	profiles    map[Message]*Profile
	config      Config
	globalAlarm chan bool
}

func NewDetector(config Config, context context.Context) *Detector {
	d := &Detector{
		context:     context,
		profiles:    make(map[Message]*Profile),
		config:      config,
		globalAlarm: make(chan bool),
	}

	if config.EnableGlobalAlarmForwarding {
		go d.listenForGlobalAlarm()
	}

	return d
}

func (d *Detector) listenForGlobalAlarm() {
	for {
		select {
		case <-d.context.Done():
			return
		case alarm := <-d.globalAlarm:
			if alarm {
				// log.Println("Detector Alarm Triggered")
			}
		}
	}
}

func (d *Detector) HandleMessage(id Message) {
	p, ok := d.profiles[id]
	if !ok {
		p = &Profile{
			ID:     id,
			Config: d.config,
			Buffer: make([]time.Time, 0, d.config.BatchSize+1),
			P:      d.config.InitialP,
			ch:     make(chan Message),
			alert:  make(chan bool, 1),
			IsInit: true,
			K:      0.5,
		}
		d.profiles[id] = p

		// Start the message listener
		go p.Listen(d.context)

		if d.config.EnableGlobalAlarmForwarding {
			go p.forwardToGlobalAlarm(d.context, d.globalAlarm)
		}
	}

	p.ch <- id
}
