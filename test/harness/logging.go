package harness

import (
	"fmt"
	"os"
)

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
