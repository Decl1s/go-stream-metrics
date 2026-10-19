package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func call(args ...string) (int, string, string) {
	var out, err bytes.Buffer
	code := execute(args, &out, &err)
	return code, out.String(), err.String()
}

func TestDemoAndRunCLI(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "requests.jsonl")
	output := filepath.Join(dir, "summary.json")
	if code, _, err := call("demo", "-output", input); code != 0 {
		t.Fatal(err)
	}
	code, out, err := call("run", "-input", input, "-output", output, "-json")
	if code != 0 {
		t.Fatal(err)
	}
	var s struct{ InputRows, AcceptedRows, RejectedRows int64 }
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(raw["input_rows"], &s.InputRows)
	json.Unmarshal(raw["accepted_rows"], &s.AcceptedRows)
	json.Unmarshal(raw["rejected_rows"], &s.RejectedRows)
	if s.InputRows != 10005 || s.AcceptedRows != 10000 || s.RejectedRows != 5 {
		t.Fatal(s)
	}
	stored, _ := os.ReadFile(output)
	if !bytes.Equal(stored, []byte(out)) {
		t.Fatal("Файл отличается от JSON stdout")
	}
}

func TestExistingDemoAndOutputArePreserved(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "requests.jsonl")
	if code, _, err := call("demo", "-output", input, "-rows", "3"); code != 0 {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(input)
	if code, _, _ := call("demo", "-output", input); code == 0 {
		t.Fatal("Демо заменено")
	}
	if code, _, _ := call("run", "-input", input, "-output", input); code == 0 {
		t.Fatal("Вход заменён результатом")
	}
	after, _ := os.ReadFile(input)
	if !bytes.Equal(before, after) {
		t.Fatal("Файл изменился")
	}
}

func TestFailedAggregateDoesNotCreateOutput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "in.jsonl")
	output := filepath.Join(dir, "out.json")
	os.WriteFile(input, []byte(strings.Repeat("x", 16384)), 0600)
	if code, _, _ := call("run", "-input", input, "-output", output); code == 0 {
		t.Fatal("Сбой не обнаружен")
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Создан ложный результат")
	}
}

func TestPartialWriteIsCleanedUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.json")
	err := writeExclusive(path, func(w io.Writer) error { w.Write([]byte("partial")); return errors.New("сбой") })
	if err == nil {
		t.Fatal("Ошибка потеряна")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Остался частичный файл")
	}
}

func TestSymlinkPathsAreRejected(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.jsonl")
	link := filepath.Join(dir, "link.jsonl")
	os.WriteFile(target, []byte(""), 0600)
	if err := os.Symlink(target, link); err != nil {
		t.Skip("Нет прав на символическую ссылку")
	}
	if code, _, _ := call("run", "-input", link); code == 0 {
		t.Fatal("Ссылка прочитана")
	}
	if err := writeExclusive(link, func(w io.Writer) error { return nil }); err == nil {
		t.Fatal("Ссылка заменена")
	}
}

func TestHelpRussianErrorsAndInvalidArguments(t *testing.T) {
	if code, out, _ := call("help"); code != 0 || !strings.Contains(out, "Потоковые метрики") {
		t.Fatal(out)
	}
	for _, args := range [][]string{{"unknown"}, {"run", "-unknown"}, {"demo", "-rows", "0"}, {"run", "extra"}, {"run", "-input", filepath.Join(t.TempDir(), "missing.jsonl")}} {
		code, _, err := call(args...)
		if code == 0 || !strings.Contains(err, "Ошибка:") {
			t.Fatal(args, err)
		}
		if strings.Contains(err, "missing.jsonl") {
			t.Fatal("Выведен приватный путь")
		}
	}
}

func TestDemoGenerationIsDeterministic(t *testing.T) {
	var first, second bytes.Buffer
	if err := generate(&first, 17); err != nil {
		t.Fatal(err)
	}
	generate(&second, 17)
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("Демо не воспроизводится")
	}
}
