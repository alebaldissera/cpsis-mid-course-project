package cids_polling

import (
	"context"
	"image/color"
	"os"
	"testing"
	"time"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
	"gonum.org/v1/plot/vg/vgimg"
)

func TestBaseline(t *testing.T) {
	config := DefaultConfig()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	detector := NewDetector(config, ctx)
	msgID := Message(0x100)
	period := 10 * time.Millisecond

	// Simulated clock skew: 50ppm
	trueSkew := 0.000050

	startTime := time.Now()

	batches := 1000

	for i := 0; i < batches; i++ {
		for j := 0; j < config.BatchSize; j++ {
			drift := time.Duration(float64(period) * trueSkew)

			// Calculate the time until the next message (period + drift + jitter - lastJitter).
			// Then subtract the time until currentTs (which is time.Until(currentTs))
			// This ensures that the next message is sent at the correct time.
			<-time.After(period + drift)

			detector.HandleMessage(msgID)
		}
	}

	if alert := <-detector.profiles[msgID].alert; alert {
		t.Log("The detector launch an alert")
	} else {
		t.Log("No alarm triggered")
	}

	var offsets plotter.XYs
	var errors plotter.XYs
	var stateH plotter.XYs
	var stateL plotter.XYs

	// Maps the history values
	for _, h := range detector.profiles[msgID].history {
		offsets = append(offsets, plotter.XY{X: h.timeStamp.Sub(startTime).Seconds(), Y: h.OAcc})
		errors = append(errors, plotter.XY{X: h.timeStamp.Sub(startTime).Seconds(), Y: h.LastEK})
		stateH = append(stateH, plotter.XY{X: h.timeStamp.Sub(startTime).Seconds(), Y: h.CusumH})
		stateL = append(stateL, plotter.XY{X: h.timeStamp.Sub(startTime).Seconds(), Y: h.CusumL})
	}

	if len(offsets) == 0 {
		t.Fatal("No data points recorded")
	}

	// Plot: Offset
	p1 := plot.New()
	p1.Title.Text = "Accumulated Clock Offset (O_acc) - Baseline"
	p1.X.Label.Text = "Time (s)"
	p1.Y.Label.Text = "Offset (s)"

	offsetLine, err := plotter.NewLine(offsets)
	if err != nil {
		t.Fatal(err)
	}
	offsetLine.Color = color.RGBA{R: 34, G: 139, B: 34, A: 255} // ForestGreen
	p1.Add(offsetLine)
	p1.Legend.Add("Accumulated Offset", offsetLine)
	p1.Legend.Top = true
	p1.Legend.Left = true

	// Add space on the right side of the axis
	if len(offsets) > 0 {
		maxX := offsets[len(offsets)-1].X
		p1.X.Max = maxX * 1.1
	}

	p2 := plot.New()
	p2.Title.Text = "Identification Error (e_k) - Baseline"
	p2.X.Label.Text = "Time (s)"
	p2.Y.Label.Text = "Error"

	errorLine, err := plotter.NewLine(errors)
	if err != nil {
		t.Fatal(err)
	}
	errorLine.Color = color.RGBA{R: 255, G: 0, B: 0, A: 255} // Red
	p2.Add(errorLine)
	p2.Legend.Add("Identification Error", errorLine)
	p2.Legend.Top = true
	p2.Legend.Left = true

	// Add space on the right side of the axis
	if len(errors) > 0 {
		maxX := errors[len(errors)-1].X
		p2.X.Max = maxX * 1.1
	}

	p3 := plot.New()
	p3.Title.Text = "CUSUM Detector State (L^+) - Baseline"
	p3.X.Label.Text = "Time (s)"
	p3.Y.Label.Text = "L^+"

	stateHLine, err := plotter.NewLine(stateH)
	stateLLine, err := plotter.NewLine(stateL)
	if err != nil {
		t.Fatal(err)
	}
	stateHLine.Color = color.RGBA{R: 0, G: 0, B: 255, A: 255} // Blue
	stateLLine.Color = color.RGBA{R: 0, G: 255, B: 0, A: 255} // Green
	p3.Add(stateHLine)
	p3.Add(stateLLine)
	p3.Legend.Add("CUSUM H Detector State", stateHLine)
	p3.Legend.Add("CUSUM L Detector State", stateLLine)
	p3.Legend.Top = true
	p3.Legend.Left = true

	// Add space on the right side of the axis
	if len(stateH) > 0 {
		maxX := stateH[len(stateH)-1].X
		p3.X.Max = maxX * 1.1
	}

	// Layout
	img := vgimg.New(10*vg.Inch, 10*vg.Inch)
	dc := draw.New(img)
	// Crop adds padding: left, right, bottom, top
	paddedDC := draw.Crop(dc, 0, -20*vg.Millimeter, 0, 0)

	t_layout := draw.Tiles{Rows: 3, Cols: 1, PadY: vg.Millimeter * 5}
	canvases := plot.Align([][]*plot.Plot{{p1}, {p2}, {p3}}, t_layout, paddedDC)
	p1.Draw(canvases[0][0])
	p2.Draw(canvases[1][0])
	p3.Draw(canvases[2][0])

	// Save
	f, err := os.Create("baseline_chart.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png := vgimg.PngCanvas{Canvas: img}
	if _, err := png.WriteTo(f); err != nil {
		t.Fatal(err)
	}

	t.Log("Baseline chart saved to baseline_chart.png")
}

func TestFabricationAttack(t *testing.T) {
	config := DefaultConfig()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	detector := NewDetector(config, ctx)
	msgID := Message(0x100)
	period := 10 * time.Millisecond

	// Simulated clock skew: 50ppm
	trueSkew := 0.000050

	startTime := time.Now()

	initBatches := 500
	recoveryBatches := 250

	for i := 0; i < initBatches; i++ {
		for j := 0; j < config.BatchSize; j++ {
			drift := time.Duration(float64(period) * trueSkew)
			<-time.After(period + drift)
			detector.HandleMessage(msgID)
		}
	}

	// Attack phase - half normal batch + 1 injection + half batch normal
	for i := 0; i < config.BatchSize/2; i++ {
		drift := time.Duration(float64(period) * trueSkew)
		<-time.After(period + drift)
		detector.HandleMessage(msgID)
	}

	// Injection of a fabricated message
	attackerSkew := 0.000070                // 70 ppm
	injcetionDelay := 30 * time.Millisecond // 1ms of arbirtray delay for injected message
	drift := time.Duration(float64(period) * attackerSkew)
	<-time.After(period + drift + injcetionDelay)

	detector.HandleMessage(msgID)

	// Half batch normal
	for i := 0; i < config.BatchSize/2-1; i++ {
		drift := time.Duration(float64(period) * trueSkew)
		<-time.After(period + drift)
		detector.HandleMessage(msgID)
	}

	if alert := <-detector.profiles[msgID].alert; alert {
		t.Log("The detector launch an alert after fabrication of messages")
	} else {
		t.Log("No alarm triggered in fabrication attack")
	}

	// Recovery phase
	for i := 0; i < recoveryBatches; i++ {
		for j := 0; j < config.BatchSize; j++ {
			drift := time.Duration(float64(period) * trueSkew)
			<-time.After(period + drift)
			detector.HandleMessage(msgID)
		}
	}

	if alert := <-detector.profiles[msgID].alert; alert {
		t.Log("The detector launch an alert")
	} else {
		t.Log("No alarm triggered in fabrication attack")
	}

	var offsets plotter.XYs
	var errors plotter.XYs
	var stateH plotter.XYs
	var stateL plotter.XYs

	// Maps the history values
	for _, h := range detector.profiles[msgID].history {
		offsets = append(offsets, plotter.XY{X: h.timeStamp.Sub(startTime).Seconds(), Y: h.OAcc})
		errors = append(errors, plotter.XY{X: h.timeStamp.Sub(startTime).Seconds(), Y: h.LastEK})
		stateH = append(stateH, plotter.XY{X: h.timeStamp.Sub(startTime).Seconds(), Y: h.CusumH})
		stateL = append(stateL, plotter.XY{X: h.timeStamp.Sub(startTime).Seconds(), Y: h.CusumL})
	}

	if len(offsets) == 0 {
		t.Fatal("No data points recorded")
	}

	// Plot: Offset
	p1 := plot.New()
	p1.Title.Text = "Accumulated Clock Offset (O_acc) - Fabrication Attack"
	p1.X.Label.Text = "Time (s)"
	p1.Y.Label.Text = "Offset (s)"

	offsetLine, err := plotter.NewLine(offsets)
	if err != nil {
		t.Fatal(err)
	}
	offsetLine.Color = color.RGBA{R: 34, G: 139, B: 34, A: 255} // ForestGreen
	p1.Add(offsetLine)
	p1.Legend.Add("Accumulated Offset", offsetLine)
	p1.Legend.Top = true
	p1.Legend.Left = true

	// Add space on the right side of the axis
	if len(offsets) > 0 {
		maxX := offsets[len(offsets)-1].X
		p1.X.Max = maxX * 1.1
	}

	p2 := plot.New()
	p2.Title.Text = "Identification Error (e_k) - Fabrication Attack"
	p2.X.Label.Text = "Time (s)"
	p2.Y.Label.Text = "Error"

	errorLine, err := plotter.NewLine(errors)
	if err != nil {
		t.Fatal(err)
	}
	errorLine.Color = color.RGBA{R: 255, G: 0, B: 0, A: 255} // Red
	p2.Add(errorLine)
	p2.Legend.Add("Identification Error", errorLine)
	p2.Legend.Top = true
	p2.Legend.Left = true

	// Add space on the right side of the axis
	if len(errors) > 0 {
		maxX := errors[len(errors)-1].X
		p2.X.Max = maxX * 1.1
	}

	p3 := plot.New()
	p3.Title.Text = "CUSUM Detector State (L^+) - Fabrication Attack"
	p3.X.Label.Text = "Time (s)"
	p3.Y.Label.Text = "L^+"

	stateHLine, err := plotter.NewLine(stateH)
	stateLLine, err := plotter.NewLine(stateL)
	if err != nil {
		t.Fatal(err)
	}
	stateHLine.Color = color.RGBA{R: 0, G: 0, B: 255, A: 255} // Blue
	stateLLine.Color = color.RGBA{R: 0, G: 255, B: 0, A: 255} // Green
	p3.Add(stateHLine)
	p3.Add(stateLLine)
	p3.Legend.Add("CUSUM H Detector State", stateHLine)
	p3.Legend.Add("CUSUM L Detector State", stateLLine)
	p3.Legend.Top = true
	p3.Legend.Left = true

	// Add space on the right side of the axis
	if len(stateH) > 0 {
		maxX := stateH[len(stateH)-1].X
		p3.X.Max = maxX * 1.1
	}

	// Layout
	img := vgimg.New(10*vg.Inch, 10*vg.Inch)
	dc := draw.New(img)
	// Crop adds padding: left, right, bottom, top
	paddedDC := draw.Crop(dc, 0, -20*vg.Millimeter, 0, 0)

	t_layout := draw.Tiles{Rows: 3, Cols: 1, PadY: vg.Millimeter * 5}
	canvases := plot.Align([][]*plot.Plot{{p1}, {p2}, {p3}}, t_layout, paddedDC)
	p1.Draw(canvases[0][0])
	p2.Draw(canvases[1][0])
	p3.Draw(canvases[2][0])

	// Save
	f, err := os.Create("fabrication_attack_chart.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png := vgimg.PngCanvas{Canvas: img}
	if _, err := png.WriteTo(f); err != nil {
		t.Fatal(err)
	}

	t.Log("Fabrication attack chart saved to fabrication_attack_chart.png")
}

func TestSuspensionAttack(t *testing.T) {
	config := DefaultConfig()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	detector := NewDetector(config, ctx)
	msgID := Message(0x100)
	period := 10 * time.Millisecond

	// Simulated clock skew: 50ppm
	trueSkew := 0.000050

	startTime := time.Now()

	initBatches := 500

	for i := 0; i < initBatches; i++ {
		for j := 0; j < config.BatchSize; j++ {
			drift := time.Duration(float64(period) * trueSkew)
			<-time.After(period + drift)
			detector.HandleMessage(msgID)
		}
	}

	// Simulate a suspension attack by waiting for
	waitBatches := 200
	<-time.After(time.Duration(waitBatches*config.BatchSize) * period)

	if alert := <-detector.profiles[msgID].alert; alert {
		t.Log("The detector launch an alert")
	} else {
		t.Log("No alarm triggered in suspension attack")
	}

	var offsets plotter.XYs
	var errors plotter.XYs
	var stateH plotter.XYs
	var stateL plotter.XYs

	// Maps the history values
	for _, h := range detector.profiles[msgID].history {
		offsets = append(offsets, plotter.XY{X: h.timeStamp.Sub(startTime).Seconds(), Y: h.OAcc})
		errors = append(errors, plotter.XY{X: h.timeStamp.Sub(startTime).Seconds(), Y: h.LastEK})
		stateH = append(stateH, plotter.XY{X: h.timeStamp.Sub(startTime).Seconds(), Y: h.CusumH})
		stateL = append(stateL, plotter.XY{X: h.timeStamp.Sub(startTime).Seconds(), Y: h.CusumL})
	}

	if len(offsets) == 0 {
		t.Fatal("No data points recorded")
	}

	// Plot: Offset
	p1 := plot.New()
	p1.Title.Text = "Accumulated Clock Offset (O_acc) - Suspension Attack"
	p1.X.Label.Text = "Time (s)"
	p1.Y.Label.Text = "Offset (s)"

	offsetLine, err := plotter.NewLine(offsets)
	if err != nil {
		t.Fatal(err)
	}
	offsetLine.Color = color.RGBA{R: 34, G: 139, B: 34, A: 255} // ForestGreen
	p1.Add(offsetLine)
	p1.Legend.Add("Accumulated Offset", offsetLine)
	p1.Legend.Top = true
	p1.Legend.Left = true

	// Add space on the right side of the axis
	if len(offsets) > 0 {
		maxX := offsets[len(offsets)-1].X
		p1.X.Max = maxX * 1.1
	}

	p2 := plot.New()
	p2.Title.Text = "Identification Error (e_k) - Suspension Attack"
	p2.X.Label.Text = "Time (s)"
	p2.Y.Label.Text = "Error"

	errorLine, err := plotter.NewLine(errors)
	if err != nil {
		t.Fatal(err)
	}
	errorLine.Color = color.RGBA{R: 255, G: 0, B: 0, A: 255} // Red
	p2.Add(errorLine)
	p2.Legend.Add("Identification Error", errorLine)
	p2.Legend.Top = true
	p2.Legend.Left = true

	// Add space on the right side of the axis
	if len(errors) > 0 {
		maxX := errors[len(errors)-1].X
		p2.X.Max = maxX * 1.1
	}

	p3 := plot.New()
	p3.Title.Text = "CUSUM Detector State (L^+) - Suspension Attack"
	p3.X.Label.Text = "Time (s)"
	p3.Y.Label.Text = "L^+"

	stateHLine, err := plotter.NewLine(stateH)
	stateLLine, err := plotter.NewLine(stateL)
	if err != nil {
		t.Fatal(err)
	}
	stateHLine.Color = color.RGBA{R: 0, G: 0, B: 255, A: 255} // Blue
	stateLLine.Color = color.RGBA{R: 0, G: 255, B: 0, A: 255} // Green
	p3.Add(stateHLine)
	p3.Add(stateLLine)
	p3.Legend.Add("CUSUM H Detector State", stateHLine)
	p3.Legend.Add("CUSUM L Detector State", stateLLine)
	p3.Legend.Top = true
	p3.Legend.Left = true

	// Add space on the right side of the axis
	if len(stateH) > 0 {
		maxX := stateH[len(stateH)-1].X
		p3.X.Max = maxX * 1.1
	}

	// Layout
	img := vgimg.New(10*vg.Inch, 10*vg.Inch)
	dc := draw.New(img)
	// Crop adds padding: left, right, bottom, top
	paddedDC := draw.Crop(dc, 0, -20*vg.Millimeter, 0, 0)

	t_layout := draw.Tiles{Rows: 3, Cols: 1, PadY: vg.Millimeter * 5}
	canvases := plot.Align([][]*plot.Plot{{p1}, {p2}, {p3}}, t_layout, paddedDC)
	p1.Draw(canvases[0][0])
	p2.Draw(canvases[1][0])
	p3.Draw(canvases[2][0])

	// Save
	f, err := os.Create("suspension_attack_chart.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png := vgimg.PngCanvas{Canvas: img}
	if _, err := png.WriteTo(f); err != nil {
		t.Fatal(err)
	}

	t.Log("Suspension attack chart saved to suspension_attack_chart.png")
}

func TestMasqueradeAttack(t *testing.T) {
	config := DefaultConfig()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	detector := NewDetector(config, ctx)
	msgID := Message(0x123)
	period := 10 * time.Millisecond

	// Simulated skews
	trueSkew := 0.000050      // 50 ppm
	attackerSkew := -0.000050 // -4950 ppm (5000 ppm difference)

	startTime := time.Now()
	initBatches := 500

	// lastJitter := time.Duration(0)
	for i := 0; i < initBatches; i++ {
		for j := 0; j < config.BatchSize; j++ {
			drift := time.Duration(float64(period) * trueSkew)
			<-time.After(period + drift)
			detector.HandleMessage(msgID)
		}
	}

	attackBatches := 500
	for i := 0; i < attackBatches; i++ {
		for j := 0; j < config.BatchSize; j++ {
			drift := time.Duration(float64(period) * attackerSkew)
			<-time.After(period + drift)
			detector.HandleMessage(msgID)
		}
	}

	if alert := <-detector.profiles[msgID].alert; alert {
		t.Log("The detector launch an alert")
	} else {
		t.Log("No alarm triggered in masquerade attack")
	}

	var offsets plotter.XYs
	var errors plotter.XYs
	var stateH plotter.XYs
	var stateL plotter.XYs

	// Maps the history values
	for _, h := range detector.profiles[msgID].history {
		offsets = append(offsets, plotter.XY{X: h.timeStamp.Sub(startTime).Seconds(), Y: h.OAcc})
		errors = append(errors, plotter.XY{X: h.timeStamp.Sub(startTime).Seconds(), Y: h.LastEK})
		stateH = append(stateH, plotter.XY{X: h.timeStamp.Sub(startTime).Seconds(), Y: h.CusumH})
		stateL = append(stateL, plotter.XY{X: h.timeStamp.Sub(startTime).Seconds(), Y: h.CusumL})
	}

	if len(offsets) == 0 {
		t.Fatal("No data points recorded")
	}

	// Plot: Offset
	p1 := plot.New()
	p1.Title.Text = "Accumulated Clock Offset (O_acc) - Masquerade Attack"
	p1.X.Label.Text = "Time (s)"
	p1.Y.Label.Text = "Offset (s)"

	offsetLine, err := plotter.NewLine(offsets)
	if err != nil {
		t.Fatal(err)
	}
	offsetLine.Color = color.RGBA{R: 34, G: 139, B: 34, A: 255} // ForestGreen
	p1.Add(offsetLine)
	p1.Legend.Add("Accumulated Offset", offsetLine)
	p1.Legend.Top = true
	p1.Legend.Left = true

	// Add space on the right side of the axis
	if len(offsets) > 0 {
		maxX := offsets[len(offsets)-1].X
		p1.X.Max = maxX * 1.1
	}

	p2 := plot.New()
	p2.Title.Text = "Identification Error (e_k) - Masquerade Attack"
	p2.X.Label.Text = "Time (s)"
	p2.Y.Label.Text = "Error"

	errorLine, err := plotter.NewLine(errors)
	if err != nil {
		t.Fatal(err)
	}
	errorLine.Color = color.RGBA{R: 255, G: 0, B: 0, A: 255} // Red
	p2.Add(errorLine)
	p2.Legend.Add("Identification Error", errorLine)
	p2.Legend.Top = true
	p2.Legend.Left = true

	// Add space on the right side of the axis
	if len(errors) > 0 {
		maxX := errors[len(errors)-1].X
		p2.X.Max = maxX * 1.1
	}

	p3 := plot.New()
	p3.Title.Text = "CUSUM Detector State (L^+) - Masquerade Attack"
	p3.X.Label.Text = "Time (s)"
	p3.Y.Label.Text = "L^+"

	stateHLine, err := plotter.NewLine(stateH)
	stateLLine, err := plotter.NewLine(stateL)
	if err != nil {
		t.Fatal(err)
	}
	stateHLine.Color = color.RGBA{R: 0, G: 0, B: 255, A: 255} // Blue
	stateLLine.Color = color.RGBA{R: 0, G: 255, B: 0, A: 255} // Green
	p3.Add(stateHLine)
	p3.Add(stateLLine)
	p3.Legend.Add("CUSUM H Detector State", stateHLine)
	p3.Legend.Add("CUSUM L Detector State", stateLLine)
	p3.Legend.Top = true
	p3.Legend.Left = true

	// Add space on the right side of the axis
	if len(stateH) > 0 {
		maxX := stateH[len(stateH)-1].X
		p3.X.Max = maxX * 1.1
	}

	// Layout
	img := vgimg.New(10*vg.Inch, 10*vg.Inch)
	dc := draw.New(img)
	// Crop adds padding: left, right, bottom, top
	paddedDC := draw.Crop(dc, 0, -20*vg.Millimeter, 0, 0)

	t_layout := draw.Tiles{Rows: 3, Cols: 1, PadY: vg.Millimeter * 5}
	canvases := plot.Align([][]*plot.Plot{{p1}, {p2}, {p3}}, t_layout, paddedDC)
	p1.Draw(canvases[0][0])
	p2.Draw(canvases[1][0])
	p3.Draw(canvases[2][0])

	// Save
	f, err := os.Create("masquerade_attack_chart.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png := vgimg.PngCanvas{Canvas: img}
	if _, err := png.WriteTo(f); err != nil {
		t.Fatal(err)
	}

	t.Log("Masquerade attack chart saved to masquerade_attack_chart.png")
}
