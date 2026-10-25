// Command buildtime prints the time to stamp into a homeskillet build, in RFC
// 3339 format in UTC, for the Makefile to pass with -ldflags. Make 3.81 has no
// clock, and the Makefile's recipes call only Go tools.
//
// When SOURCE_DATE_EPOCH is set, as reproducible-build systems do, it prints
// that time instead of the current one, so the same source and settings always
// produce the same binary.
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

func main() {
	now := time.Now()
	if epoch := os.Getenv("SOURCE_DATE_EPOCH"); epoch != "" {
		seconds, err := strconv.ParseInt(epoch, 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "buildtime: SOURCE_DATE_EPOCH=%q is not a number of seconds\n", epoch)
			os.Exit(1)
		}
		now = time.Unix(seconds, 0)
	}
	fmt.Println(now.UTC().Format(time.RFC3339))
}
