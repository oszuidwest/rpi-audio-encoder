package audio

import "time"

// PeakHoldDuration is how long peaks are held before decay.
const PeakHoldDuration = 1500 * time.Millisecond

// PeakHolder tracks peak-hold state for VU meters.
type PeakHolder struct {
	heldPeakL     float64
	heldPeakR     float64
	peakHoldTimeL time.Time
	peakHoldTimeR time.Time
}

// NewPeakHolder creates a new peak holder initialized to minimum levels.
func NewPeakHolder() *PeakHolder {
	return &PeakHolder{
		heldPeakL: MinDB,
		heldPeakR: MinDB,
	}
}

// Update updates held peaks based on current peaks and time.
// Returns the held peak values.
func (p *PeakHolder) Update(peakL, peakR float64, now time.Time) (heldL, heldR float64) {
	// Update held peak if current is higher, or hold time expired
	// Each channel has independent hold timing
	if peakL >= p.heldPeakL || now.Sub(p.peakHoldTimeL) > PeakHoldDuration {
		p.heldPeakL = peakL
		p.peakHoldTimeL = now
	}
	if peakR >= p.heldPeakR || now.Sub(p.peakHoldTimeR) > PeakHoldDuration {
		p.heldPeakR = peakR
		p.peakHoldTimeR = now
	}
	return p.heldPeakL, p.heldPeakR
}

// Reset resets peak hold to minimum levels.
func (p *PeakHolder) Reset() {
	p.heldPeakL = MinDB
	p.heldPeakR = MinDB
	p.peakHoldTimeL = time.Time{}
	p.peakHoldTimeR = time.Time{}
}
