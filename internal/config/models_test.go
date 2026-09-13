package config

import (
	"strings"
	"testing"
)

// A provider's models are tried in the order given, so order is priority and
// the first entry is the default.
func TestLoadParsesMultipleModelsInOrder(t *testing.T) {
	env := validEnv()
	env["GEMINI_MODELS"] = "gemini-2.5-flash, gemini-2.5-pro ,gemini-3-flash"
	withEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Providers[0].Models
	want := []string{"gemini-2.5-flash", "gemini-2.5-pro", "gemini-3-flash"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("models = %v, want %v", got, want)
	}
	if got := cfg.Providers[0].Default(); got != "gemini-2.5-flash" {
		t.Fatalf("Default() = %q", got)
	}
	if !cfg.Providers[0].HasModel("gemini-2.5-pro") {
		t.Fatal("HasModel should find a listed model")
	}
	if cfg.Providers[0].HasModel("GEMINI-2.5-PRO") {
		t.Fatal("HasModel must compare exactly; model IDs are case-sensitive")
	}
}

// A repeated ID only wastes a fallback slot, so it is reported and dropped.
func TestLoadRejectsDuplicateModels(t *testing.T) {
	env := validEnv()
	env["GEMINI_MODELS"] = "gemini-2.5-flash,gemini-2.5-pro,gemini-2.5-flash"
	withEnv(t, env)

	_, err := Load()
	if err == nil {
		t.Fatal("expected a duplicate model to be reported")
	}
	if !strings.Contains(err.Error(), "gemini-2.5-flash") {
		t.Fatalf("error should name the duplicated model:\n%s", err)
	}
}

// A list of separators and spaces is an empty list, not a valid one.
func TestLoadRejectsModelListWithNoEntries(t *testing.T) {
	env := validEnv()
	env["GEMINI_MODELS"] = " , ,"
	withEnv(t, env)

	_, err := Load()
	if err == nil {
		t.Fatal("expected an empty model list to be reported")
	}
	if !strings.Contains(err.Error(), "GEMINI_MODELS") {
		t.Fatalf("error should name GEMINI_MODELS:\n%s", err)
	}
}

// Without YUNA_SUMMARY_MODEL the summariser keeps using its provider's default,
// which is the behaviour callers had before the setting existed.
func TestSummaryModelDefaultsToProviderDefault(t *testing.T) {
	env := validEnv()
	env["YUNA_SUMMARY_PROVIDER"] = "groq"
	env["GROQ_MODELS"] = "llama-3.3-70b-versatile,llama-3.1-8b-instant"
	withEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SummaryModel != "llama-3.3-70b-versatile" {
		t.Fatalf("SummaryModel = %q, want the provider default", cfg.SummaryModel)
	}
}

func TestSummaryModelAcceptsListedModel(t *testing.T) {
	env := validEnv()
	env["YUNA_SUMMARY_PROVIDER"] = "groq"
	env["GROQ_MODELS"] = "llama-3.3-70b-versatile,llama-3.1-8b-instant"
	env["YUNA_SUMMARY_MODEL"] = "llama-3.1-8b-instant"
	withEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SummaryModel != "llama-3.1-8b-instant" {
		t.Fatalf("SummaryModel = %q", cfg.SummaryModel)
	}
}

// A summary model that belongs to no configured provider would only fail later,
// on the first background refresh, so Load refuses it.
func TestSummaryModelRejectsUnlistedModel(t *testing.T) {
	env := validEnv()
	env["YUNA_SUMMARY_PROVIDER"] = "groq"
	env["YUNA_SUMMARY_MODEL"] = "gpt-5.5"
	withEnv(t, env)

	_, err := Load()
	if err == nil {
		t.Fatal("expected an unlisted summary model to be reported")
	}
	if !strings.Contains(err.Error(), "YUNA_SUMMARY_MODEL") {
		t.Fatalf("error should name YUNA_SUMMARY_MODEL:\n%s", err)
	}
}

// YUNA_SUMMARY_PROVIDER defaults to the last entry in the chain, and the summary
// model follows that provider rather than the primary's.
func TestSummaryModelFollowsDefaultedProvider(t *testing.T) {
	env := validEnv()
	env["GROQ_MODELS"] = "llama-3.3-70b-versatile,llama-3.1-8b-instant"
	withEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SummaryProvider != "groq" {
		t.Fatalf("SummaryProvider = %q, want the last chain entry", cfg.SummaryProvider)
	}
	if cfg.SummaryModel != "llama-3.3-70b-versatile" {
		t.Fatalf("SummaryModel = %q, want groq's default", cfg.SummaryModel)
	}
}
