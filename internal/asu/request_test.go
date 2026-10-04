package asu

import "testing"

func strptr(s string) *string { return &s }

// The expected hashes below are taken verbatim from the upstream ASU test
// suite (tests/test_api.py) to guarantee cross-implementation compatibility.
func TestRequestHashMatchesASU(t *testing.T) {
	cases := []struct {
		name string
		req  BuildRequest
		want string
	}{
		{
			name: "diff_packages_true",
			req: BuildRequest{
				Version:      strptr("1.2.3"),
				Target:       strptr("testtarget/testsubtarget"),
				Profile:      strptr("testprofile"),
				Packages:     []string{"test1", "zzz", "test2", "aaa"},
				DiffPackages: true,
			},
			want: "1c4a79c6b711a576996cf9a5e7046a4581008c4466574096266f0e6ea4208fbc",
		},
		{
			name: "diff_packages_false",
			req: BuildRequest{
				Version:  strptr("1.2.3"),
				Target:   strptr("testtarget/testsubtarget"),
				Profile:  strptr("testprofile"),
				Packages: []string{"test1", "zzz", "test2", "aaa"},
			},
			want: "c5a849e05b60611b465042594fc3489a44f7695c3d09e36433a577ee772ad7b7",
		},
		{
			name: "defaults_script",
			req: BuildRequest{
				Version:  strptr("1.2.3"),
				Target:   strptr("testtarget/testsubtarget"),
				Profile:  strptr("testprofile"),
				Defaults: strptr("echo"),
			},
			want: "ba50558496f8fead41e8d5bc72afd1ad7d27bc053afb550a8bf6ee3bbcc64952",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.req.RequestHash()
			if got != tc.want {
				t.Fatalf("request hash mismatch\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestPackagesHashIgnoresOrderAndPlus(t *testing.T) {
	a := packagesHash([]string{"+vim", "tmux"})
	b := packagesHash([]string{"tmux", "vim"})
	if a != b {
		t.Fatalf("packages hash should be order independent: %s != %s", a, b)
	}
}

func TestPythonLiteralFormatting(t *testing.T) {
	if got := pythonList([]string{"a", "b"}); got != "['a', 'b']" {
		t.Fatalf("pythonList = %q", got)
	}
	if got := pythonList(nil); got != "[]" {
		t.Fatalf("pythonList empty = %q", got)
	}
	if got := pythonMap(map[string]string{}); got != "{}" {
		t.Fatalf("pythonMap empty = %q", got)
	}
	if got := jsonDumps(map[string]string{"test2": "2.0", "test1": "1.0"}); got != `{"test1": "1.0", "test2": "2.0"}` {
		t.Fatalf("jsonDumps = %q", got)
	}
}
