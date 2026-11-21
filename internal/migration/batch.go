package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var batchColumnPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)
var placeholderPattern = regexp.MustCompile(`\{\{([a-z_]+)\}\}`)

func normalizeBatchConfig(mode string, cfg BatchConfig) (BatchConfig, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ModeSingle:
		return BatchConfig{}, nil
	case ModeBatch:
		if !batchColumnPattern.MatchString(cfg.Column) {
			return BatchConfig{}, fmt.Errorf("%w: batch column must be a simple identifier", InvalidInput)
		}
		if cfg.Size < 1 || cfg.Size > 100000 {
			return BatchConfig{}, fmt.Errorf("%w: batch size must be between 1 and 100000", InvalidInput)
		}
		return cfg, nil
	default:
		return BatchConfig{}, fmt.Errorf("%w: mode must be single or batch", InvalidInput)
	}
}

func validateBatchSQL(mode string, upSQL, downSQL string) error {
	if mode == ModeSingle {
		for _, match := range placeholderPattern.FindAllStringSubmatch(upSQL+"\n"+downSQL, -1) {
			return fmt.Errorf("%w: placeholder %s is only allowed in batch mode", InvalidInput, match[0])
		}
		return nil
	}
	if mode != ModeBatch {
		return fmt.Errorf("%w: mode must be single or batch", InvalidInput)
	}
	for _, text := range []string{upSQL, downSQL} {
		for _, match := range placeholderPattern.FindAllStringSubmatch(text, -1) {
			switch match[1] {
			case "batch_column", "last_id", "batch_size", "range_from", "range_to":
			default:
				return fmt.Errorf("%w: unknown placeholder %s", InvalidInput, match[0])
			}
		}
	}
	for _, name := range []string{"batch_column", "last_id", "batch_size"} {
		if !strings.Contains(upSQL, "{{"+name+"}}") {
			return fmt.Errorf("%w: batch up SQL must use {{%s}}", InvalidInput, name)
		}
	}
	if strings.TrimSpace(downSQL) != "" {
		for _, name := range []string{"batch_column", "range_from", "range_to"} {
			if !strings.Contains(downSQL, "{{"+name+"}}") {
				return fmt.Errorf("%w: batch down SQL must use {{%s}}", InvalidInput, name)
			}
		}
	}
	return nil
}

func revisionHash(upSQL, downSQL string, irreversible bool, reason, mode string, cfg BatchConfig) string {
	cfgBytes, _ := json.Marshal(cfg)
	sum := sha256.Sum256([]byte(strings.Join([]string{upSQL, downSQL, fmt.Sprintf("%t", irreversible), reason, mode, string(cfgBytes)}, "\x00")))
	return hex.EncodeToString(sum[:])
}