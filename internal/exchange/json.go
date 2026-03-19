package exchange

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func WriteExchange(path string, e Exchange) error {
	if e.Protocol == "" {
		e.Protocol = ExchangeProtocolV1
	}
	return writeJSON(path, e)
}

func ReadExchange(path string) (Exchange, error) {
	var e Exchange
	if err := readJSON(path, &e); err != nil {
		return Exchange{}, err
	}
	return e, nil
}

func WriteReturn(path string, r Return) error {
	if r.Protocol == "" {
		r.Protocol = ReturnProtocolV1
	}
	return writeJSON(path, r)
}

func ReadReturn(path string) (Return, error) {
	var r Return
	if err := readJSON(path, &r); err != nil {
		return Return{}, err
	}
	return r, nil
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json for %s: %w", path, err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func readJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
