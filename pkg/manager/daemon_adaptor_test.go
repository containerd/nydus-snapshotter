package manager

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseSystemdRunServiceUnit(t *testing.T) {
	testCases := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{
			name:   "single line output",
			output: "Running as unit: run-rc28cf5fdc45c497cbe6736aea1e2701e.service\n",
			want:   "run-rc28cf5fdc45c497cbe6736aea1e2701e.service",
		},
		{
			name: "warning before unit line",
			output: "Failed to add PIDs to scope's control group: Operation not permitted\n" +
				"Running as unit: run-rc28cf5fdc45c497cbe6736aea1e2701e.service\n",
			want: "run-rc28cf5fdc45c497cbe6736aea1e2701e.service",
		},
		{
			name: "trim surrounding whitespace",
			output: "\n  Running as unit: run-rc28cf5fdc45c497cbe6736aea1e2701e.service   \n" +
				"Journal has been rotated since unit was started.\n",
			want: "run-rc28cf5fdc45c497cbe6736aea1e2701e.service",
		},
		{
			name:    "missing unit line",
			output:  "Failed to add PIDs to scope's control group: Operation not permitted\n",
			wantErr: true,
		},
		{
			name:    "empty unit line",
			output:  "Running as unit:\n",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSystemdRunServiceUnit(tc.output)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got service unit %q", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("parseSystemdRunServiceUnit() error = %v", err)
			}

			if got != tc.want {
				t.Fatalf("parseSystemdRunServiceUnit() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseSystemdMainPID(t *testing.T) {
	testCases := []struct {
		name            string
		output          string
		want            int
		wantErrIs       error
		wantErrContains string
	}{
		{
			name:   "single line output",
			output: "MainPID=1037947\n",
			want:   1037947,
		},
		{
			name: "extra lines around property",
			output: "Some warning\n" +
				"MainPID=1037947\n" +
				"Trailing text\n",
			want: 1037947,
		},
		{
			name:      "pid not ready",
			output:    "MainPID=0\n",
			wantErrIs: errSystemdMainPIDNotReady,
		},
		{
			name:            "missing property",
			output:          "ExecMainPID=1037947\n",
			wantErrContains: "failed to find MainPID",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSystemdMainPID(tc.output)
			if tc.wantErrIs != nil || tc.wantErrContains != "" {
				if err == nil {
					t.Fatalf("expected error, got pid %d", got)
				}
				if tc.wantErrIs != nil {
					if !errors.Is(err, tc.wantErrIs) {
						t.Fatalf("parseSystemdMainPID() error = %v, want %v", err, tc.wantErrIs)
					}
					return
				}
				if !strings.Contains(err.Error(), tc.wantErrContains) {
					t.Fatalf("parseSystemdMainPID() error = %v, want substring %q", err, tc.wantErrContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("parseSystemdMainPID() error = %v", err)
			}

			if got != tc.want {
				t.Fatalf("parseSystemdMainPID() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParseNydusdPidFromServiceUnitRetriesUntilPositivePID(t *testing.T) {
	origShowMainPID := systemdShowMainPID
	origAttempts := systemdMainPIDPollAttempts
	origDelay := systemdMainPIDPollDelay
	t.Cleanup(func() {
		systemdShowMainPID = origShowMainPID
		systemdMainPIDPollAttempts = origAttempts
		systemdMainPIDPollDelay = origDelay
	})

	systemdMainPIDPollAttempts = 5
	systemdMainPIDPollDelay = time.Millisecond

	calls := 0
	systemdShowMainPID = func(serviceUnit string) ([]byte, error) {
		calls++
		if serviceUnit != "demo.service" {
			t.Fatalf("unexpected service unit %q", serviceUnit)
		}
		if calls < 3 {
			return []byte("MainPID=0\n"), nil
		}
		return []byte("MainPID=42\n"), nil
	}

	pid, err := parseNydusdPidFromServiceUnit("demo.service")
	if err != nil {
		t.Fatalf("parseNydusdPidFromServiceUnit() error = %v", err)
	}
	if pid != 42 {
		t.Fatalf("parseNydusdPidFromServiceUnit() = %d, want 42", pid)
	}
	if calls != 3 {
		t.Fatalf("systemdShowMainPID calls = %d, want 3", calls)
	}
}

func TestParseNydusdPidFromServiceUnitTimesOutOnZeroMainPID(t *testing.T) {
	origShowMainPID := systemdShowMainPID
	origAttempts := systemdMainPIDPollAttempts
	origDelay := systemdMainPIDPollDelay
	t.Cleanup(func() {
		systemdShowMainPID = origShowMainPID
		systemdMainPIDPollAttempts = origAttempts
		systemdMainPIDPollDelay = origDelay
	})

	systemdMainPIDPollAttempts = 3
	systemdMainPIDPollDelay = time.Millisecond

	calls := 0
	systemdShowMainPID = func(serviceUnit string) ([]byte, error) {
		calls++
		if serviceUnit != "demo.service" {
			t.Fatalf("unexpected service unit %q", serviceUnit)
		}
		return []byte("MainPID=0\n"), nil
	}

	pid, err := parseNydusdPidFromServiceUnit("demo.service")
	if err == nil {
		t.Fatalf("expected error, got pid %d", pid)
	}
	if pid != 0 {
		t.Fatalf("parseNydusdPidFromServiceUnit() = %d, want 0 on timeout", pid)
	}
	if !strings.Contains(err.Error(), "timed out waiting for positive MainPID") {
		t.Fatalf("parseNydusdPidFromServiceUnit() error = %v", err)
	}
	if calls != 3 {
		t.Fatalf("systemdShowMainPID calls = %d, want 3", calls)
	}
}
