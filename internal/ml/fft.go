package ml

import (
	"math"
	"math/cmplx"
	"sort"
)

// FFTAnalyzer detects periodic patterns in time series using Fast Fourier Transform.
// It identifies dominant frequencies (daily, weekly cycles) that inform the
// Holt-Winters seasonal period and improve forecast confidence.
type FFTAnalyzer struct{}

func NewFFTAnalyzer() *FFTAnalyzer {
	return &FFTAnalyzer{}
}

// DominantPeriod represents a detected periodic cycle
type DominantPeriod struct {
	PeriodPoints int     // cycle length in data points
	Amplitude    float64 // strength of the cycle (higher = stronger)
	Phase        float64 // phase offset in radians
	Power        float64 // spectral power (proportion of total variance explained)
}

// DetectPeriods analyzes a time series and returns the dominant periodic patterns.
// data should be evenly sampled. Returns periods sorted by power (strongest first).
func (f *FFTAnalyzer) DetectPeriods(data []float64, maxPeriods int) []DominantPeriod {
	n := len(data)
	if n < 8 {
		return nil
	}

	// Pad to next power of 2 for FFT efficiency
	paddedLen := nextPow2(n)
	padded := make([]float64, paddedLen)
	copy(padded, data)
	// Zero-pad remaining (already zero from make)

	// Remove mean (DC component)
	mean := 0.0
	for _, v := range data {
		mean += v
	}
	mean /= float64(n)
	for i := range padded {
		if i < n {
			padded[i] -= mean
		}
	}

	// Compute FFT
	spectrum := fft(padded)

	// Compute power spectrum (magnitude squared)
	totalPower := 0.0
	type freqPower struct {
		idx   int
		power float64
		amp   float64
		phase float64
	}
	var powers []freqPower

	// Only look at first half (Nyquist)
	halfN := paddedLen / 2
	for k := 1; k < halfN; k++ { // skip DC (k=0)
		amp := cmplx.Abs(spectrum[k]) / float64(n)
		power := amp * amp
		phase := cmplx.Phase(spectrum[k])
		totalPower += power
		powers = append(powers, freqPower{idx: k, power: power, amp: amp, phase: phase})
	}

	if totalPower == 0 {
		return nil
	}

	// Sort by power descending
	sort.Slice(powers, func(i, j int) bool {
		return powers[i].power > powers[j].power
	})

	// Take top maxPeriods
	if maxPeriods > len(powers) {
		maxPeriods = len(powers)
	}

	var result []DominantPeriod
	for i := 0; i < maxPeriods; i++ {
		fp := powers[i]
		period := paddedLen / fp.idx // period in data points

		// Skip very short periods (noise) and very long periods (unreliable)
		if period < 3 || period > n/2 {
			continue
		}

		result = append(result, DominantPeriod{
			PeriodPoints: period,
			Amplitude:    fp.amp,
			Phase:        fp.phase,
			Power:        fp.power / totalPower,
		})
	}

	return result
}

// DetectSeasonLength analyzes the time series and returns the best seasonal period.
// samplesPerHour indicates how many data points represent one hour.
// Returns 0 if no clear seasonality is detected.
func (f *FFTAnalyzer) DetectSeasonLength(data []float64, samplesPerHour float64) int {
	periods := f.DetectPeriods(data, 5)
	if len(periods) == 0 {
		return 0
	}

	// Look for periods that correspond to common cycles
	bestPeriod := 0
	bestPower := 0.0

	for _, p := range periods {
		hoursInPeriod := float64(p.PeriodPoints) / samplesPerHour

		// Check if this matches common infrastructure cycles
		isDaily := hoursInPeriod > 20 && hoursInPeriod < 28       // ~24h
		isWeekly := hoursInPeriod > 140 && hoursInPeriod < 196    // ~168h
		isHalfDay := hoursInPeriod > 10 && hoursInPeriod < 14     // ~12h

		if (isDaily || isWeekly || isHalfDay) && p.Power > bestPower {
			bestPower = p.Power
			bestPeriod = p.PeriodPoints
		}
	}

	// If no common cycle detected, use the strongest period if it's significant
	if bestPeriod == 0 && periods[0].Power > 0.1 {
		bestPeriod = periods[0].PeriodPoints
	}

	return bestPeriod
}

// fft computes the Discrete Fourier Transform using Cooley-Tukey radix-2 algorithm.
// Input length must be a power of 2.
func fft(x []float64) []complex128 {
	n := len(x)
	if n == 1 {
		return []complex128{complex(x[0], 0)}
	}

	// Split into even and odd
	even := make([]float64, n/2)
	odd := make([]float64, n/2)
	for i := 0; i < n/2; i++ {
		even[i] = x[2*i]
		odd[i] = x[2*i+1]
	}

	// Recursive FFT
	fEven := fft(even)
	fOdd := fft(odd)

	// Combine
	result := make([]complex128, n)
	for k := 0; k < n/2; k++ {
		// Twiddle factor: W_n^k = e^(-2πik/n)
		angle := -2 * math.Pi * float64(k) / float64(n)
		w := cmplx.Rect(1, angle)
		result[k] = fEven[k] + w*fOdd[k]
		result[k+n/2] = fEven[k] - w*fOdd[k]
	}

	return result
}

// nextPow2 returns the smallest power of 2 >= n
func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}
