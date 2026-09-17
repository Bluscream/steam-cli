package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"steamcli.local/steam/internal/auth"
)

func authCommand(o *options) *cobra.Command {
	root := &cobra.Command{
		Use:     "auth",
		Aliases: []string{"login"},
		Short:   "Authenticate steamcli with Steam Community and manage web sessions",
	}

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

	// Interactive default when invoking 'steamcli auth'
	interactiveCmd := &cobra.Command{
		Use:   "interactive",
		Short: "Choose between QR code or browser authentication",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInteractiveAuth(cmd, o)
		},
	}

	root.RunE = func(cmd *cobra.Command, args []string) error {
		return runInteractiveAuth(cmd, o)
	}

	root.AddCommand(qrCmd, browserCmd, interactiveCmd)
	return root
}

func runInteractiveAuth(cmd *cobra.Command, o *options) error {
	out := cmd.OutOrStdout()
	in := cmd.InOrStdin()

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		// Non-interactive: default to QR code
		return runQRAuth(cmd, o)
	}

	fmt.Fprintln(out, "Select a login method for steam-cli:")
	fmt.Fprintln(out, "  1) Steam Mobile QR Code (Instant terminal scan)")
	fmt.Fprintln(out, "  2) Browser Loopback (Sign in via web browser)")
	fmt.Fprint(out, "Choice [1/2, default 1]: ")

	reader := bufio.NewReader(in)
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	if choice == "2" {
		return runBrowserAuth(cmd, o)
	}
	return runQRAuth(cmd, o)
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
	sess, err := client.PollQR(ctx, clientID, requestID, interval)
	if err != nil {
		return fmt.Errorf("login failed or timed out: %w", err)
	}

	if err := auth.SaveSession(sess); err != nil {
		return fmt.Errorf("save session: %w", err)
	}

	return o.emit(cmd, sess, func(w io.Writer) {
		fmt.Fprintf(w, "\n✓ Successfully authenticated as %s (SteamID: %s)!\n", sess.AccountName, sess.SteamID)
		fmt.Fprintf(w, "Session cookie saved to ~/.local/share/steam-cli/steam_login_secure\n")
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
		fmt.Fprintf(w, "Session cookie saved to ~/.local/share/steam-cli/steam_login_secure\n")
	})
}
