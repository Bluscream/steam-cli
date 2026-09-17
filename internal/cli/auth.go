package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"steamcli.local/steam/internal/auth"
)

func authCommand(o *options) *cobra.Command {
	var userFlag, passFlag, codeFlag string

	root := &cobra.Command{
		Use:     "auth",
		Aliases: []string{"login"},
		Short:   "Authenticate steamcli with Steam Community and manage web sessions",
	}

	credentialsCmd := &cobra.Command{
		Use:     "login [USERNAME]",
		Aliases: []string{"credentials", "user"},
		Short:   "Log in directly using Steam username and password",
		RunE: func(cmd *cobra.Command, args []string) error {
			user := userFlag
			if user == "" && len(args) > 0 {
				user = args[0]
			}
			return runCredentialsAuth(cmd, o, user, passFlag, codeFlag)
		},
	}
	credentialsCmd.Flags().StringVarP(&userFlag, "user", "u", "", "Steam account username")
	credentialsCmd.Flags().StringVarP(&passFlag, "password", "p", "", "Steam account password")
	credentialsCmd.Flags().StringVarP(&codeFlag, "code", "c", "", "Steam Guard 2FA confirmation code (optional)")

	qrCmd := &cobra.Command{
		Use:   "qr",
		Short: "Log in by scanning a QR code with the Steam Mobile App",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runQRAuth(cmd, o)
		},
	}

	browserCmd := &cobra.Command{
		Use:   "browser",
		Short: "Log in via browser window and local loopback session receiver",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBrowserAuth(cmd, o)
		},
	}

	root.RunE = func(cmd *cobra.Command, args []string) error {
		return runCredentialsAuth(cmd, o, userFlag, passFlag, codeFlag)
	}

	root.AddCommand(credentialsCmd, qrCmd, browserCmd)
	return root
}

func runCredentialsAuth(cmd *cobra.Command, o *options, username, password, code string) error {
	out := cmd.OutOrStdout()
	in := cmd.InOrStdin()
	reader := bufio.NewReader(in)

	if username == "" {
		fmt.Fprint(out, "Steam Username: ")
		u, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		username = strings.TrimSpace(u)
	}
	if username == "" {
		return errors.New("username cannot be empty")
	}

	if password == "" {
		fmt.Fprint(out, "Steam Password: ")
		if term.IsTerminal(int(os.Stdin.Fd())) {
			bytePass, err := term.ReadPassword(int(syscall.Stdin))
			fmt.Fprintln(out)
			if err != nil {
				return err
			}
			password = string(bytePass)
		} else {
			p, err := reader.ReadString('\n')
			if err != nil {
				return err
			}
			password = strings.TrimSpace(p)
		}
	}
	if password == "" {
		return errors.New("password cannot be empty")
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 4*time.Minute)
	defer cancel()

	client := auth.NewClient(o.http())

	fmt.Fprintln(out, "Initiating secure Steam credentials login...")
	chal, err := client.BeginCredentials(ctx, username, password)
	if err != nil {
		return fmt.Errorf("authentication error: %w", err)
	}

	// If Steam requires confirmation (Steam Guard mobile or code)
	needsCode := false
	codeType := 3 // default to 2-factor code
	for _, conf := range chal.AllowedConfirmations {
		if conf == 2 {
			needsCode = true
			codeType = 2 // Email code
			break
		}
		if conf == 3 {
			needsCode = true
			codeType = 3 // TOTP code
		}
	}

	if needsCode {
		if code == "" {
			if codeType == 2 {
				fmt.Fprint(out, "Enter the Steam Guard code sent to your email: ")
			} else {
				fmt.Fprint(out, "Enter your Steam Guard 2FA code (or confirm in your Steam Mobile App): ")
			}
			if term.IsTerminal(int(os.Stdin.Fd())) {
				c, _ := reader.ReadString('\n')
				code = strings.TrimSpace(c)
			}
		}
		if code != "" {
			fmt.Fprintln(out, "Submitting Steam Guard code...")
			if err := client.SubmitSteamGuardCode(ctx, chal.ClientID, chal.SteamID, code, codeType); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: code submission: %v\n", err)
			}
		}
	} else if len(chal.AllowedConfirmations) > 0 {
		fmt.Fprintln(out, "Please confirm the login prompt in your Steam Mobile App...")
	}

	fmt.Fprintln(out, "Finalizing session...")
	sess, err := client.PollSession(ctx, chal.ClientID, chal.RequestID, chal.Interval)
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	if sess.AccountName == "" {
		sess.AccountName = username
	}
	if sess.SteamID == "" && chal.SteamID != "" {
		sess.SteamID = chal.SteamID
		if sess.AccessToken != "" {
			sess.SteamLoginSecure = sess.SteamID + "%7C%7C" + sess.AccessToken
		}
	}

	if err := auth.SaveSession(sess); err != nil {
		return fmt.Errorf("save session: %w", err)
	}

	return o.emit(cmd, sess, func(w io.Writer) {
		fmt.Fprintf(w, "\n✓ Successfully authenticated as %s (SteamID: %s)!\n", sess.AccountName, sess.SteamID)
		fmt.Fprintf(w, "Session saved to ~/.local/share/steam-cli/steam_login_secure\n")
	})
}

func runQRAuth(cmd *cobra.Command, o *options) error {
	out := cmd.OutOrStdout()
	ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Minute)
	defer cancel()

	client := auth.NewClient(o.http())

	fmt.Fprintln(out, "Requesting Steam QR login session...")
	clientID, challengeURL, requestID, interval, err := client.BeginQR(ctx)
	if err != nil {
		return fmt.Errorf("initialize QR session: %w", err)
	}

	fmt.Fprintln(out, "\nScan the QR code below with the Steam Mobile App:")
	fmt.Fprintf(out, "(Or open: %s)\n\n", challengeURL)

	if err := auth.RenderTerminalQR(out, challengeURL); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not render terminal QR: %v\n", err)
	}

	fmt.Fprintln(out, "Waiting for confirmation in Steam Mobile...")
	sess, err := client.PollSession(ctx, clientID, requestID, interval)
	if err != nil {
		return fmt.Errorf("login failed or timed out: %w", err)
	}

	if err := auth.SaveSession(sess); err != nil {
		return fmt.Errorf("save session: %w", err)
	}

	return o.emit(cmd, sess, func(w io.Writer) {
		fmt.Fprintf(w, "\n✓ Successfully authenticated as %s (SteamID: %s)!\n", sess.AccountName, sess.SteamID)
		fmt.Fprintf(w, "Session saved to ~/.local/share/steam-cli/steam_login_secure\n")
	})
}

func runBrowserAuth(cmd *cobra.Command, o *options) error {
	out := cmd.OutOrStdout()
	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
	defer cancel()

	fmt.Fprintln(out, "Opening browser for Steam authorization...")
	fmt.Fprintln(out, "If the browser does not open automatically, visit: http://127.0.0.1:20888/")

	sess, err := auth.BrowserFlow(ctx, 20888)
	if err != nil {
		return fmt.Errorf("browser auth: %w", err)
	}
	if sess.SteamLoginSecure == "" {
		return errors.New("no session cookie was received")
	}

	if err := auth.SaveSession(sess); err != nil {
		return fmt.Errorf("save session: %w", err)
	}

	return o.emit(cmd, sess, func(w io.Writer) {
		fmt.Fprintf(w, "\n✓ Successfully authorized session (SteamID: %s)!\n", sess.SteamID)
		fmt.Fprintf(w, "Session saved to ~/.local/share/steam-cli/steam_login_secure\n")
	})
}
