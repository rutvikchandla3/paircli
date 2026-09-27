package classify

import (
	"testing"
)

func TestSuppression(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		want  string
		found bool
	}{
		{
			name:  "eslint disable",
			line:  "// eslint-disable-next-line",
			want:  "eslint-disable",
			found: true,
		},
		{
			name:  "biome ignore",
			line:  "// biome-ignore lint",
			want:  "biome-ignore",
			found: true,
		},
		{
			name:  "ts ignore",
			line:  "// @ts-ignore",
			want:  "@ts-ignore",
			found: true,
		},
		{
			name:  "ts expect error",
			line:  "// @ts-expect-error",
			want:  "@ts-expect-error",
			found: true,
		},
		{
			name:  "ts nocheck",
			line:  "// @ts-nocheck",
			want:  "@ts-nocheck",
			found: true,
		},
		{
			name:  "type ignore",
			line:  "# type: ignore",
			want:  "#\\s+type:\\s+ignore",
			found: true,
		},
		{
			name:  "pylint disable",
			line:  "# pylint: disable=line-too-long",
			want:  "pylint:\\s+disable",
			found: true,
		},
		{
			name:  "nolint",
			line:  "// nolint",
			want:  "nolint",
			found: true,
		},
		{
			name:  "no suppression",
			line:  "// regular comment",
			want:  "",
			found: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := Suppression(tt.line)
			if found != tt.found {
				t.Errorf("Suppression(%q) found=%v, want %v", tt.line, found, tt.found)
			}
			if found && got == "" {
				t.Errorf("Suppression(%q) returned empty kind when found=true", tt.line)
			}
		})
	}
}

func TestSkipMarker(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		want  string
		found bool
	}{
		{
			name:  "test only",
			line:  "it.only('should work', () => {})",
			want:  "only",
			found: true,
		},
		{
			name:  "describe only",
			line:  "describe.only('suite', () => {})",
			want:  "only",
			found: true,
		},
		{
			name:  "fit",
			line:  "fit('test', () => {})",
			want:  "only",
			found: true,
		},
		{
			name:  "test skip",
			line:  "test.skip('should skip', () => {})",
			want:  "skip",
			found: true,
		},
		{
			name:  "xit",
			line:  "xit('skipped test', () => {})",
			want:  "skip",
			found: true,
		},
		{
			name:  "pytest skip",
			line:  "@pytest.mark.skip",
			want:  "skip",
			found: true,
		},
		{
			name:  "pytest xfail",
			line:  "@pytest.mark.xfail",
			want:  "xfail",
			found: true,
		},
		{
			name:  "test todo",
			line:  "it.todo('future test')",
			want:  "todo",
			found: true,
		},
		{
			name:  "no marker",
			line:  "it('should work', () => {})",
			want:  "",
			found: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := SkipMarker(tt.line)
			if found != tt.found || (found && got != tt.want) {
				t.Errorf("SkipMarker(%q) = (%q, %v), want (%q, %v)", tt.line, got, found, tt.want, tt.found)
			}
		})
	}
}

func TestAssertionLike(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{
			name: "expect assertion",
			line: "expect(result).toBe(true)",
			want: true,
		},
		{
			name: "assert assertion",
			line: "assert.equal(a, b)",
			want: true,
		},
		{
			name: "t.Fatal",
			line: "t.Fatal(err)",
			want: true,
		},
		{
			name: "should assertion",
			line: "result.should.be.ok",
			want: true,
		},
		{
			name: "XCTAssert",
			line: "XCTAssertEqual(a, b)",
			want: true,
		},
		{
			name: "no assertion",
			line: "const result = compute()",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AssertionLike(tt.line)
			if got != tt.want {
				t.Errorf("AssertionLike(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}
