package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"steamcli.local/steam/internal/sdk"
)

func sdkCommand(o *options) *cobra.Command {
	var dir, library, compiler, appID string
	root := &cobra.Command{Use: "sdk", Short: "Discover and invoke native Steamworks SDK methods"}
	root.PersistentFlags().StringVar(&dir, "sdk-dir", os.Getenv("STEAM_SDK_DIR"), "Local SDK directory containing public/steam/steam_api.json")
	root.PersistentFlags().StringVar(&library, "library", os.Getenv("STEAM_SDK_LIBRARY"), "Absolute path to a compatible native Steam API runtime")
	root.PersistentFlags().StringVar(&appID, "appid", "", "AppID for native client initialization; requires Steam access to that app")
	loaded := func() (sdk.Build, error) {
		s, e := o.settings()
		if e != nil {
			return sdk.Build{}, e
		}
		b, e := sdk.Load(s.DataDir)
		if e != nil {
			return b, e
		}
		if library != "" {
			b.Library = library
		}
		return b, nil
	}
	schema := func() (sdk.Schema, []byte, error) {
		p := dir
		if p == "" {
			b, e := loaded()
			if e != nil {
				return sdk.Schema{}, nil, e
			}
			p = b.SDKDir
		}
		return sdk.ReadSchema(p)
	}
	build := &cobra.Command{Use: "build", Short: "Build and cache a native helper for the supplied SDK (C++17 compiler required once)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if dir == "" {
			return errors.New("--sdk-dir or STEAM_SDK_DIR is required")
		}
		s, e := o.settings()
		if e != nil {
			return e
		}
		b, e := sdk.Compile(cmd.Context(), dir, s.DataDir, compiler, library, cmd.ErrOrStderr())
		if e != nil {
			return e
		}
		return o.print(cmd, b)
	}}
	build.Flags().StringVar(&compiler, "cxx", "", "C++17 compiler executable; defaults to CXX or c++ (clang++ on Windows)")
	methods := &cobra.Command{Use: "methods [FILTER]", Short: "Search all SDK interface and struct methods, including their native parameters", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, _, e := schema()
		if e != nil {
			return e
		}
		out := []sdk.Method{}
		for _, m := range s.Methods() {
			if len(args) == 0 || strings.Contains(strings.ToLower(m.Symbol), strings.ToLower(args[0])) {
				out = append(out, m)
			}
		}
		return o.print(cmd, out)
	}}
	reference := &cobra.Command{Use: "schema", Short: "Print the complete supplied SDK metadata, including enums, callbacks and typedefs", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		_, b, e := schema()
		if e != nil {
			return e
		}
		return o.printBytes(cmd, b)
	}}
	path := &cobra.Command{Use: "path", Short: "Show the compiled helper and selected SDK runtime", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		b, e := loaded()
		if e != nil {
			return e
		}
		return o.print(cmd, b)
	}}
	validate := func() error {
		if o.offline {
			return errors.New("native Steam sessions cannot enforce --offline; use sdk methods/schema/build for offline work")
		}
		n, e := strconv.ParseUint(appID, 10, 32)
		if e != nil || n == 0 {
			return errors.New("--appid must be an explicit positive AppID for native initialization")
		}
		return nil
	}
	var params, self string
	call := &cobra.Command{Use: "call INTERFACE METHOD", Short: "Invoke a native SDK method; use a session for handles, buffers or callbacks", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		if e := validate(); e != nil {
			return e
		}
		s, _, e := schema()
		if e != nil {
			return e
		}
		m, e := s.Resolve(args[0], args[1])
		if e != nil {
			return e
		}
		var values []json.RawMessage
		if e = json.Unmarshal([]byte(params), &values); e != nil {
			return fmt.Errorf("--args must be a JSON array: %w", e)
		}
		if values == nil {
			values = []json.RawMessage{}
		}
		if len(values) != len(m.Params) {
			return fmt.Errorf("%s expects %d arguments; use sdk methods to inspect their types", m.Symbol, len(m.Params))
		}
		request := map[string]any{"op": "call", "method": m.Symbol, "args": values}
		if self != "" {
			var value any
			if e = json.Unmarshal([]byte(self), &value); e != nil {
				return e
			}
			request["self"] = value
		}
		input := bytes.NewBufferString("{\"op\":\"init\"}\n")
		json.NewEncoder(input).Encode(request)
		b, e := loaded()
		if e != nil {
			return e
		}
		var output bytes.Buffer
		runErr := sdk.Run(cmd.Context(), b, appID, input, &output, cmd.ErrOrStderr())
		scanner := bufio.NewScanner(&output)
		scanner.Buffer(make([]byte, 4096), 32<<20)
		var result json.RawMessage
		for scanner.Scan() {
			var response struct {
				OK     bool            `json:"ok"`
				Result json.RawMessage `json:"result"`
				Error  string          `json:"error"`
			}
			if e = json.Unmarshal(scanner.Bytes(), &response); e != nil {
				return fmt.Errorf("invalid native helper response: %w", e)
			}
			if !response.OK {
				return errors.New(response.Error)
			}
			result = append(result[:0], response.Result...)
		}
		if e = scanner.Err(); e != nil {
			return e
		}
		if runErr != nil {
			return runErr
		}
		if len(result) == 0 {
			return errors.New("native helper returned no result")
		}
		return o.printBytes(cmd, result)
	}}
	call.Flags().StringVar(&params, "args", "[]", "Native parameters as a JSON array; encode 64-bit IDs as decimal strings")
	call.Flags().StringVar(&self, "self", "", "Explicit self buffer/handle JSON (normally use sdk session for this)")
	var noInit bool
	session := &cobra.Command{Use: "session", Short: "Run a persistent JSON-lines native session on stdin/stdout", Long: "Run a persistent native session. Each input line is one JSON request; each reply carries ok, result/error and optional id.\nUse call with an exact flat method symbol, buffer/read/write, layout, and poll.\nSteam diagnostics go to stderr. Handles and buffers exist only for this process.\nSession output is always JSON Lines regardless of --output.", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if !noInit {
			if e := validate(); e != nil {
				return e
			}
		} else if o.offline {
			return errors.New("native session cannot enforce --offline")
		}
		b, e := loaded()
		if e != nil {
			return e
		}
		var input io.Reader = cmd.InOrStdin()
		if !noInit {
			input = io.MultiReader(strings.NewReader("{\"op\":\"init\"}\n"), input)
		}
		return sdk.Run(cmd.Context(), b, appID, input, cmd.OutOrStdout(), cmd.ErrOrStderr())
	}}
	session.Flags().BoolVar(&noInit, "no-init", false, "Supply initialization explicitly in the JSON protocol (or inspect buffers/layout only)")
	root.AddCommand(build, methods, reference, path, call, session)
	return root
}
