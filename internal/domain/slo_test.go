package domain

import (
	"testing"
)

func TestComputeSLOMode(t *testing.T) {
	tests := []struct {
		name           string
		budgetPct      float64
		burnRates      BurnRates
		expectedMode   SLOMode
	}{
		{
			name:      "Normal — all healthy",
			budgetPct: 80,
			burnRates: BurnRates{Window1h: 0.5, Window6h: 0.3, Window1d: 0.2, Window3d: 0.1},
			expectedMode: SLOModeNormal,
		},
		{
			name:      "Emergency — budget below 5%",
			budgetPct: 3,
			burnRates: BurnRates{Window1h: 1.0, Window6h: 0.5, Window1d: 0.3, Window3d: 0.1},
			expectedMode: SLOModeEmergency,
		},
		{
			name:      "Critical — budget below 20%",
			budgetPct: 15,
			burnRates: BurnRates{Window1h: 2.0, Window6h: 1.0, Window1d: 0.5, Window3d: 0.3},
			expectedMode: SLOModeCritical,
		},
		{
			name:      "Critical — high 1h burn rate",
			budgetPct: 60,
			burnRates: BurnRates{Window1h: 15.0, Window6h: 3.0, Window1d: 1.0, Window3d: 0.5},
			expectedMode: SLOModeCritical,
		},
		{
			name:      "Critical — high 6h burn rate",
			budgetPct: 60,
			burnRates: BurnRates{Window1h: 4.0, Window6h: 7.0, Window1d: 2.0, Window3d: 0.8},
			expectedMode: SLOModeCritical,
		},
		{
			name:      "Warning — elevated 1d burn rate",
			budgetPct: 60,
			burnRates: BurnRates{Window1h: 2.0, Window6h: 2.0, Window1d: 4.0, Window3d: 0.5},
			expectedMode: SLOModeWarning,
		},
		{
			name:      "Warning — elevated 3d burn rate",
			budgetPct: 60,
			burnRates: BurnRates{Window1h: 0.5, Window6h: 0.5, Window1d: 0.5, Window3d: 1.5},
			expectedMode: SLOModeWarning,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode := ComputeSLOMode(tt.budgetPct, tt.burnRates)
			if mode != tt.expectedMode {
				t.Errorf("got %v, want %v", mode, tt.expectedMode)
			}
		})
	}
}
