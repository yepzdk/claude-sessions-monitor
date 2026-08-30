package session

import "testing"

// The path is the rest of the n line, so a directory name holding a space is
// part of it. Read as the last whitespace-separated field, "/Users/x/My Project"
// comes back as "Project", and the session is then keyed under a project that
// does not exist, so it disappears from the dashboard.
//
// The same parser reads the controlling terminal off `-d 0`, which is why the
// device-path cases are here too.
func TestParseLsofNameKeepsAPathThatHoldsSpaces(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string // empty means the parse must fail
	}{
		{
			name: "an ordinary path",
			out:  "p1\nfcwd\nn/Users/u/proj\n",
			want: "/Users/u/proj",
		},
		{
			name: "a path holding spaces",
			out:  "p1\nfcwd\nn/Users/u/My Project\n",
			want: "/Users/u/My Project",
		},
		{
			name: "a controlling terminal, as -d 0 prints it",
			out:  "p1\nf0\nn/dev/ttys003\n",
			want: "/dev/ttys003",
		},
		{
			name: "no path line",
			out:  "p1\nfcwd\n",
		},
		{
			name: "no output at all",
			out:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLsofName([]byte(tt.out))
			if tt.want == "" {
				if err == nil {
					t.Fatalf("parsed %q out of output with no usable name line", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLsofName: %v", err)
			}
			if got != tt.want {
				t.Errorf("name = %q, want %q", got, tt.want)
			}
		})
	}
}
