package main

import (
	"fmt"
	"os"
	"strconv"
)

func fizzbuzz(n int) string {
	switch {
	case n%15 == 0:
		return "fizzbuzz"
	case n%3 == 0:
		return "fizz"
	case n%5 == 0:
		return "buzz"
	default:
		return strconv.Itoa(n)
	}
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <num1> <num2>\n", os.Args[0])
		os.Exit(1)
	}

	a, err := strconv.Atoi(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid first argument: %s\n", os.Args[1])
		os.Exit(1)
	}

	b, err := strconv.Atoi(os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid second argument: %s\n", os.Args[2])
		os.Exit(1)
	}

	sum := a + b
	fmt.Println(fizzbuzz(sum))
}
