package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/vsriram/simple-host/internal/handler"
)

const reviewAccountUsage = `usage: simple-host review-account hash

  hash  Read a password from standard input (one line) and print the value for
        REVIEW_ACCOUNT_PASSWORD_HASH. The password must be at least 16
        characters. Nothing is stored and no database is needed.

        read -rs PW && printf '%s\n' "$PW" | simple-host review-account hash; unset PW

Then set, in /etc/simple-host.env, and restart:
  REVIEW_ACCOUNT_EMAIL=<the reviewer account's email>
  REVIEW_ACCOUNT_PASSWORD_HASH=<the printed value>
Unset both to turn reviewer sign-in off again.`

func runReviewAccountCommand(args []string) int {
	if len(args) != 1 || args[0] != "hash" {
		os.Stderr.WriteString(reviewAccountUsage + "\n")
		return 2
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(os.Stderr, "no password on standard input")
		return 2
	}
	password := strings.TrimRight(line, "\r\n")
	hash, err := handler.HashReviewerPassword(password)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	fmt.Println(hash)
	return 0
}
