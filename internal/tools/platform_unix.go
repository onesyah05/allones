//go:build !windows

package tools

import "os"

func pathSep() rune      { return ':' }
func sep() rune          { return '/' }
func execExts() []string { return []string{""} }
func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}
