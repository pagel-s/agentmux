package managed

import (
	"strings"
	"unicode"
)

func validateImportedClaims(source MemorySource) *Fault {
	claims := source.SourceSession != "" || source.SourceAgent != "" || source.SourceNamespace != "" || source.SourceAuthor != "" || source.SourceKey != "" || source.SourceVersion != 0 || source.ArchiveSHA256 != ""
	if !claims {
		return nil
	} // Existing v1 import references remain readable.
	if source.Kind != "import" {
		return fault("invalid", "archive identity claims require an import source")
	}
	for _, field := range []struct {
		value string
		limit int
	}{
		{source.SourceSession, 64}, {source.SourceAgent, 64}, {source.SourceNamespace, 128}, {source.SourceAuthor, 64}, {source.SourceKey, 128},
	} {
		if len(field.value) == 0 || len(field.value) > field.limit || strings.IndexFunc(field.value, unicode.IsControl) >= 0 {
			return fault("invalid", "import source identity claims are incomplete or invalid")
		}
	}
	if source.SourceVersion < 1 || len(source.ArchiveSHA256) != 64 || strings.Trim(source.ArchiveSHA256, "0123456789abcdef") != "" {
		return fault("invalid", "import source requires a version and full archive digest")
	}
	return nil
}
