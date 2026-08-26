package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"ytmusic/internal/web"

	"golang.org/x/term"
)

// runHashPassword prints a bcrypt hash for the given password and returns.
// Prompts go to stderr so that `ytmusic-web -hash-password > hash.txt` writes
// only the hash to the file.
func runHashPassword() error {
	if flag.NArg() > 0 {
		return errors.New("do not pass the password as an argument: it would land in your shell history and be visible in ps.\n" +
			"Run 'ytmusic-web -hash-password' with no argument and type it when prompted")
	}

	password, err := readPassword()
	if err != nil {
		return err
	}

	hash, err := web.HashPassword(password)
	if err != nil {
		return err
	}

	fmt.Println(hash)
	return nil
}

func readPassword() (string, error) {
	fd := int(os.Stdin.Fd())

	// Not a terminal: the password is being piped in, so read it plainly and
	// skip the confirmation there is no one to answer.
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("reading password from stdin: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	fmt.Fprint(os.Stderr, "Password: ")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}

	// A typo here would only surface at the first failed login, after editing
	// the config and restarting: cheaper to catch it now.
	fmt.Fprint(os.Stderr, "Confirm:  ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading confirmation: %w", err)
	}

	if string(first) != string(second) {
		return "", errors.New("passwords do not match")
	}

	return string(first), nil
}
