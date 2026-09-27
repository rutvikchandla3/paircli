package classify

import (
	"testing"

	"github.com/rutvikchandla3/paircli/internal/model"
)

func TestCheck(t *testing.T) {
	tests := []struct {
		name string
		cmd  *model.Command
		want CheckResult
	}{
		{
			name: "jest output",
			cmd: &model.Command{
				ExitCode: intPtr(1),
				Output:   "Tests: 2 failed, 60 passed, 62 total",
			},
			want: CheckResult{
				Status:  "fail",
				Passed:  60,
				Failed:  2,
				Skipped: -1,
			},
		},
		{
			name: "pytest output",
			cmd: &model.Command{
				ExitCode: nil,
				Status:   model.CmdOK,
				Output:   "=== 1 failed, 5 passed, 2 skipped in 0.3s ===",
			},
			want: CheckResult{
				Status:  "pass",
				Passed:  5,
				Failed:  1,
				Skipped: 2,
			},
		},
		{
			name: "mocha output",
			cmd: &model.Command{
				ExitCode: intPtr(0),
				Output:   "62 passing\n2 failing",
			},
			want: CheckResult{
				Status:  "pass",
				Passed:  62,
				Failed:  2,
				Skipped: -1,
			},
		},
		{
			name: "cargo test output",
			cmd: &model.Command{
				ExitCode: intPtr(0),
				Output:   "test result: ok. 10 passed; 0 failed; 1 ignored",
			},
			want: CheckResult{
				Status:  "pass",
				Passed:  10,
				Failed:  0,
				Skipped: 1,
			},
		},
		{
			name: "node --test output",
			cmd: &model.Command{
				ExitCode: intPtr(0),
				Output:   "# pass 3\n# fail 0",
			},
			want: CheckResult{
				Status:  "pass",
				Passed:  3,
				Failed:  0,
				Skipped: -1,
			},
		},
		{
			name: "rspec output",
			cmd: &model.Command{
				ExitCode: intPtr(1),
				Output:   "12 examples, 1 failure, 2 pending",
			},
			want: CheckResult{
				Status:  "fail",
				Passed:  9,
				Failed:  1,
				Skipped: 2,
			},
		},
		{
			name: "go test with FAIL",
			cmd: &model.Command{
				ExitCode: intPtr(1),
				Output:   "--- FAIL: TestX\n--- FAIL: TestY",
			},
			want: CheckResult{
				Status:  "fail",
				Passed:  -1,
				Failed:  2,
				Skipped: -1,
			},
		},
		{
			name: "unknown with passed count",
			cmd: &model.Command{
				Status: model.CmdUnknown,
				Output: "3 passed",
			},
			want: CheckResult{
				Status:  "pass",
				Passed:  3,
				Failed:  -1,
				Skipped: -1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Check(tt.cmd)
			if got.Status != tt.want.Status ||
				got.Passed != tt.want.Passed ||
				got.Failed != tt.want.Failed ||
				got.Skipped != tt.want.Skipped {
				t.Errorf("Check(%v) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func intPtr(i int) *int {
	return &i
}
