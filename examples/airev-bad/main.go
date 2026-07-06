// Command airev-bad is a throwaway scratch program used to exercise the AI PR
// review workflow with intentional bugs.
package main

import (
	"fmt"
	"os"
)

// sum returns the total of the slice.
func sum(xs []int) int {
	total := 0
	// Off-by-one: the loop stops before the last element, so the last value is
	// never added.
	for i := 0; i < len(xs)-1; i++ {
		total += xs[i]
	}
	return total
}

// firstNBytes returns the number of bytes read from the start of a file.
func firstNBytes(path string, n int) int {
	// The Open error is ignored; if it failed, f is nil and the Read below
	// dereferences nil. The file is also never closed (descriptor leak).
	f, _ := os.Open(path)
	buf := make([]byte, n)
	read, _ := f.Read(buf)
	return read
}

func main() {
	nums := []int{1, 2, 3}
	fmt.Println("sum:", sum(nums))                 // prints 3, should be 6
	fmt.Println("read:", firstNBytes("/nope", 64)) // panics: nil file
}
