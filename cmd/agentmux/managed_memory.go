package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"agentmux/internal/managed"
	"agentmux/internal/memoryio"
)

func runManagedMemory(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	usage := func() {
		fmt.Fprintln(stderr, "Usage: agentmux managed memory <export|import|search|index|prune> --credential-file FILE [--namespace NAME] [--output FILE|--input FILE|--query TEXT] [--replace] [--embedding-base-url URL --embedding-model ID --allow-provider-calls]")
	}
	if len(args) < 1 {
		usage()
		return exitValidation
	}
	op := args[0]
	if op == "--help" || op == "help" {
		usage()
		return exitSuccess
	}
	flags := flag.NewFlagSet("managed memory "+op, flag.ContinueOnError)
	flags.SetOutput(stderr)
	credentialPath := flags.String("credential-file", "", "explicit broker credential")
	state := flags.String("state", "", "managed state directory")
	agent := flags.String("agent", "", "agent credential selected by owner")
	namespace := flags.String("namespace", "private", "authorized memory namespace")
	output := flags.String("output", "", "new private export file")
	input := flags.String("input", "", "private archive")
	query := flags.String("query", "", "retrieval query")
	replace := flags.Bool("replace", false, "CAS replace existing destination keys")
	base := flags.String("embedding-base-url", "", "explicit OpenAI-compatible embeddings API base")
	model := flags.String("embedding-model", "", "exact embedding model id")
	keyEnv := flags.String("api-key-env", "", "explicit credential environment name")
	allow := flags.Bool("allow-provider-calls", false, "authorize sending selected authorized text to embedding provider")
	maxEntries := flags.Int("max-entries", 100, "maximum embedding requests in this invocation")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
		return exitValidation
	}
	if *credentialPath == "" && *state != "" {
		abs, err := filepath.Abs(expandUserPath(*state))
		if err != nil {
			return exitValidation
		}
		*credentialPath = managed.CredentialPath(abs, *agent)
	}
	fail := func(err error) int { fmt.Fprintf(stderr, "memory: %v\n", err); return exitRuntime }
	credential, err := managed.LoadCredential(expandUserPath(*credentialPath))
	if err != nil {
		return fail(errors.New("select an available broker credential with --credential-file or --state/--agent"))
	}
	client := managed.NewClient(credential)
	var result any
	embeddingKey := ""
	if *keyEnv != "" {
		embeddingKey = os.Getenv(*keyEnv)
		if embeddingKey == "" {
			return fail(errors.New("selected embedding credential environment is unset"))
		}
	}
	if (*base != "" || *model != "") && (!*allow || *base == "" || *model == "") {
		return fail(errors.New("embedding requires an explicit API base, model, and --allow-provider-calls"))
	}
	switch op {
	case "export":
		if *output == "" {
			usage()
			return exitValidation
		}
		a, err := memoryio.Export(ctx, client, *namespace)
		if err != nil {
			return fail(err)
		}
		if err := memoryio.Write(expandUserPath(*output), a); err != nil {
			return fail(err)
		}
		result = map[string]any{"entries": len(a.Entries), "sha256": a.SHA256, "source_session": a.SourceSession}
	case "import":
		if *input == "" {
			usage()
			return exitValidation
		}
		a, err := memoryio.Read(expandUserPath(*input))
		if err != nil {
			return fail(err)
		}
		r, err := memoryio.Import(ctx, client, a, *namespace, *replace)
		if err != nil {
			return fail(err)
		}
		result = map[string]any{"imported": r.Imported, "skipped": r.Skipped, "expired": r.Expired}
	case "search":
		args := map[string]any{"namespace": *namespace, "query": *query, "limit": 20}
		if *base != "" {
			vector, err := memoryio.Embed(ctx, *base, *model, embeddingKey, *query)
			if err != nil {
				return fail(err)
			}
			args["embedding"], args["embedding_model"] = vector, *model
		}
		raw, err := memoryio.Call(ctx, client, "memory.search", args, "")
		if err != nil {
			return fail(err)
		}
		result = json.RawMessage(raw)
	case "index":
		if *base == "" || !*allow || *maxEntries < 1 || *maxEntries > 10000 {
			return fail(errors.New("index requires explicit embedding provider authorization and 1–10000 max-entries"))
		}
		a, err := memoryio.Export(ctx, client, *namespace)
		if err != nil {
			return fail(err)
		}
		if len(a.Entries) > *maxEntries {
			return fail(errors.New("authorized memory exceeds max-entries; narrow the namespace or explicitly increase the provider-call allocation"))
		}
		indexed := 0
		for _, m := range a.Entries {
			if m.EmbeddingModel == *model && !*replace {
				continue
			}
			vector, err := memoryio.Embed(ctx, *base, *model, embeddingKey, m.Value)
			if err != nil {
				return fail(err)
			}
			indexArgs := map[string]any{"namespace": *namespace, "key": m.Key, "version": m.Version, "embedding": vector, "embedding_model": *model}
			encoded, _ := json.Marshal(indexArgs)
			digest := sha256.Sum256(encoded)
			if _, err := memoryio.Call(ctx, client, "memory.index", indexArgs, fmt.Sprintf("memory_index_%x", digest)); err != nil {
				return fail(err)
			}
			indexed++
		}
		result = map[string]int{"indexed": indexed, "provider_calls": indexed}
	case "prune":
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return fail(err)
		}
		raw, err := memoryio.Call(ctx, client, "memory.prune", map[string]any{"limit": 200}, fmt.Sprintf("memory_prune_%x", nonce))
		if err != nil {
			return fail(err)
		}
		result = json.RawMessage(raw)
	default:
		usage()
		return exitValidation
	}
	if json.NewEncoder(stdout).Encode(managedEnvelope{SchemaVersion: 1, Kind: "managed", Operation: "memory." + op, OK: true, Result: result}) != nil {
		return exitRuntime
	}
	return exitSuccess
}
