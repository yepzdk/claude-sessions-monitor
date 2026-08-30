package session

import (
	"bytes"
	"errors"
)

// parseLsofName pulls the path out of `lsof -p <pid> -a -d <fd> -Fn`, which
// prints one field per line: p<pid>, f<fd>, n<path>. The path is the rest of the
// n line, spaces included; read as the last whitespace-separated field of the
// column format, "/Users/x/My Project" came back as "Project" and the session
// was keyed under a project that does not exist.
//
// Outside the darwin file so it can be tested anywhere, since lsof itself cannot
// be run in a test.
func parseLsofName(out []byte) (string, error) {
	for _, line := range bytes.Split(out, []byte("\n")) {
		if path, ok := bytes.CutPrefix(line, []byte("n")); ok && len(path) > 0 {
			return string(path), nil
		}
	}
	return "", errors.New("no name line in lsof output")
}
