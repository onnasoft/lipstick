package admin

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/OnnaSoft/lipstick/server/auth"
	"golang.org/x/term"
)

// consoleStore is the part of the account store the console commands use.
type consoleStore interface {
	CreateAdminUser(username, password, role string) error
	ResetAdminPassword(username, password string) error
	SetAdminDisabled(username string, disabled bool) error
	ListAdminUsers() ([]auth.AdminInfo, error)
}

const consoleUsage = `Usage: lipstick-server admin <command> [flags]

Commands:
  create          -username NAME [-role superuser|operator] create an admin account
  reset-password  -username NAME                         set a new password and end its sessions
  disable         -username NAME                         block the account and end its sessions
  enable          -username NAME                         allow the account again
  list                                                   list the accounts

Passwords are typed in the terminal, without echo. Run these commands on the
machine where the server is installed. They do not need any key.
`

// RunConsole runs one "admin" command from the server console and returns the exit
// code. readPassword reads a password without echo.
func RunConsole(store consoleStore, args []string, readPassword func(prompt string) (string, error), out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errOut, consoleUsage)
		return 2
	}
	command, rest := args[0], args[1:]
	fs := flag.NewFlagSet("admin "+command, flag.ContinueOnError)
	fs.SetOutput(errOut)
	username := fs.String("username", "", "account name")
	role := fs.String("role", auth.RoleSuperuser, "superuser or operator (create only)")
	if err := fs.Parse(rest); err != nil {
		return 2
	}

	switch command {
	case "list":
		return listAccounts(store, out, errOut)
	case "create", "reset-password", "disable", "enable":
		if *username == "" {
			fmt.Fprintln(errOut, "-username is required")
			return 2
		}
	default:
		fmt.Fprintf(errOut, "unknown command %q\n\n%s", command, consoleUsage)
		return 2
	}

	var err error
	switch command {
	case "create":
		var password string
		if password, err = readNewPassword(readPassword, errOut); err == nil {
			err = store.CreateAdminUser(*username, password, *role)
		}
		if err == nil {
			fmt.Fprintf(out, "created admin account %q with role %s\n", *username, *role)
		}
	case "reset-password":
		var password string
		if password, err = readNewPassword(readPassword, errOut); err == nil {
			err = store.ResetAdminPassword(*username, password)
		}
		if err == nil {
			fmt.Fprintf(out, "password of %q changed; its sessions are ended\n", *username)
		}
	case "disable", "enable":
		err = store.SetAdminDisabled(*username, command == "disable")
		if err == nil {
			fmt.Fprintf(out, "account %q is now %sd\n", *username, command)
		}
	}
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return 1
	}
	return 0
}

func listAccounts(store consoleStore, out, errOut io.Writer) int {
	users, err := store.ListAdminUsers()
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return 1
	}
	if len(users) == 0 {
		fmt.Fprintln(out, "no admin accounts; create one with: lipstick-server admin create -username NAME")
		return 0
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "USERNAME\tROLE\tSTATUS\tCREATED")
	for _, u := range users {
		status := "active"
		if u.Disabled {
			status = "disabled"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", u.Username, u.Role, status, u.CreatedAt.Format(time.DateOnly))
	}
	w.Flush()
	return 0
}

// readNewPassword asks twice and checks both answers match.
func readNewPassword(readPassword func(string) (string, error), errOut io.Writer) (string, error) {
	first, err := readPassword("New password: ")
	if err != nil {
		return "", err
	}
	second, err := readPassword("Repeat password: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("the passwords do not match")
	}
	if len(first) < auth.MinPasswordLen {
		return "", auth.ErrPasswordTooShort
	}
	return first, nil
}

// ReadTerminalPassword reads a password from the terminal without echo. It refuses
// when standard input is not a terminal, so a password is never read from a pipe or
// a script by accident.
func ReadTerminalPassword(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("run this command in a terminal on the server machine")
	}
	fmt.Fprint(os.Stderr, prompt)
	raw, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	return string(raw), err
}
