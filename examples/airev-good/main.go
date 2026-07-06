// Command airev-good is a throwaway scratch program used to exercise the AI PR
// review workflow with clean, correct code.
package main

import (
	"fmt"
	"strings"
)

// titleCase upper-cases the first letter of each whitespace-separated word.
// strings.Fields never yields empty fields, so f[:1] is always safe.
func titleCase(s string) string {
	fields := strings.Fields(s)
	for i, f := range fields {
		fields[i] = strings.ToUpper(f[:1]) + f[1:]
	}
	return strings.Join(fields, " ")
}

func main() {
	fmt.Println(titleCase("hello world from opendeezer"))
}
