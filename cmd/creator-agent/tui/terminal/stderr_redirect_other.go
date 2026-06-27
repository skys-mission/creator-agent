//go:build !(darwin || linux)

package terminal

func RedirectStderrToFile() string { return "" }
