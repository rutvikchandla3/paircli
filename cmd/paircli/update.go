package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/rutvikchandla3/paircli/internal/selfupdate"
)

func runUpdate(args []string) error {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	checkOnly := flags.Bool("check", false, "check for an update without installing it")
	quiet := flags.Bool("quiet", false, "suppress output when already current")
	jsonOutput := flags.Bool("json", false, "print the update result as JSON")
	manifestURL := flags.String("manifest-url", os.Getenv("PAIRCLI_UPDATE_MANIFEST_URL"), "release manifest URL")
	pluginVersion := flags.String("plugin-version", "", "Pair plugin version (used by the plugin integration)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("update does not accept positional arguments")
	}

	result, err := selfupdate.Run(context.Background(), selfupdate.Options{
		CurrentVersion: version,
		ManifestURL:    *manifestURL,
		PluginVersion:  *pluginVersion,
		CheckOnly:      *checkOnly,
		AllowInsecure:  os.Getenv("PAIRCLI_ALLOW_INSECURE_DOWNLOAD") == "1",
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	if result.SkippedReason != "" {
		fmt.Printf("Skipping update: %s.\n", result.SkippedReason)
		return nil
	}
	if result.Updated {
		fmt.Printf("Updated paircli from %s to %s.\n", result.CurrentVersion, result.LatestVersion)
		return nil
	}
	if *checkOnly && result.CurrentVersion != result.LatestVersion {
		fmt.Printf("Update available: %s (current %s).\n", result.LatestVersion, result.CurrentVersion)
		return nil
	}
	if !*quiet {
		fmt.Printf("paircli %s is up to date.\n", result.CurrentVersion)
	}
	return nil
}
