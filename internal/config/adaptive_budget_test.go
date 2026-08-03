package config

import (
	"os"
	"testing"
)

func TestGetAdaptiveBudget(t *testing.T) {
	tests := []struct {
		name             string
		contextWindow    string
		defaultBudget    int
		budgetPercentage float64
		expectedBudget   int
	}{
		{
			name:             "no context window env var - use default",
			contextWindow:    "",
			defaultBudget:    800,
			budgetPercentage: 0.10,
			expectedBudget:   800,
		},
		{
			name:             "context window 10000, 10% = 1000",
			contextWindow:    "10000",
			defaultBudget:    800,
			budgetPercentage: 0.10,
			expectedBudget:   1000,
		},
		{
			name:             "context window 20000, 15% = 3000",
			contextWindow:    "20000",
			defaultBudget:    800,
			budgetPercentage: 0.15,
			expectedBudget:   3000,
		},
		{
			name:             "context window 200000, 10% = 20000",
			contextWindow:    "200000",
			defaultBudget:    800,
			budgetPercentage: 0.10,
			expectedBudget:   20000,
		},
		{
			name:             "context window 1000, 10% = 100 (minimum)",
			contextWindow:    "1000",
			defaultBudget:    800,
			budgetPercentage: 0.10,
			expectedBudget:   100,
		},
		{
			name:             "context window 500, 10% = 50, clamped to minimum 100",
			contextWindow:    "500",
			defaultBudget:    800,
			budgetPercentage: 0.10,
			expectedBudget:   100,
		},
		{
			name:             "invalid context window - use default",
			contextWindow:    "not-a-number",
			defaultBudget:    800,
			budgetPercentage: 0.10,
			expectedBudget:   800,
		},
		{
			name:             "negative context window - use default",
			contextWindow:    "-1000",
			defaultBudget:    800,
			budgetPercentage: 0.10,
			expectedBudget:   800,
		},
		{
			name:             "zero budget percentage - defaults to 10%",
			contextWindow:    "10000",
			defaultBudget:    800,
			budgetPercentage: 0,
			expectedBudget:   1000,
		},
		{
			name:             "invalid budget percentage > 1.0 - defaults to 10%",
			contextWindow:    "10000",
			defaultBudget:    800,
			budgetPercentage: 1.5,
			expectedBudget:   1000,
		},
		{
			name:             "adaptive budget exceeds context window - clamped",
			contextWindow:    "5000",
			defaultBudget:    800,
			budgetPercentage: 1.5,
			expectedBudget:   500, // 1.5 is invalid, defaults to 10%, so 5000 * 0.10 = 500
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment
			if tt.contextWindow != "" {
				os.Setenv("AGENT_CONTEXT_WINDOW", tt.contextWindow)
				defer os.Unsetenv("AGENT_CONTEXT_WINDOW")
			} else {
				os.Unsetenv("AGENT_CONTEXT_WINDOW")
			}

			// Test
			got := GetAdaptiveBudget(tt.defaultBudget, tt.budgetPercentage)
			if got != tt.expectedBudget {
				t.Errorf("GetAdaptiveBudget() = %d, want %d", got, tt.expectedBudget)
			}
		})
	}
}

func TestGetAdaptiveBudget_Concurrent(t *testing.T) {
	// Test that GetAdaptiveBudget is safe for concurrent use
	os.Setenv("AGENT_CONTEXT_WINDOW", "100000")
	defer os.Unsetenv("AGENT_CONTEXT_WINDOW")

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				budget := GetAdaptiveBudget(800, 0.10)
				if budget != 10000 {
					t.Errorf("expected 10000, got %d", budget)
				}
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}
