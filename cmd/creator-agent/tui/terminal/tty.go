package terminal

import (
	"fmt"
	"os"
	"runtime"
)

func WriteOSC52(encoded string) {
	if runtime.GOOS == "windows" {
		return
	}
	f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\x1b]52;c;%s\x07", encoded)
}
