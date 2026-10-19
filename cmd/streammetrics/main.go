package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Decl1s/go-stream-metrics/internal/metrics"
)

const usage = `Потоковые метрики на Go, версия 0.1.0.
Команды:
  demo  -output workspace/requests.jsonl -rows 10000 [-json]
  run   -input workspace/requests.jsonl [-max-services 64] [-slow-ms 500] [-json] [-output новый-файл.json]
  help  показать справку
Демо не заменяет существующие файлы. run без -output выводит результат в консоль.
`

func safePath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return errors.New("Не удалось определить путь.")
	}
	for p := abs; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("Не удалось проверить путь.")
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Символические ссылки в путях не поддерживаются.")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}

// writeExclusive не заменяет существующий файл; при обрабатываемой ошибке удаляет свой незавершённый файл.
func writeExclusive(path string, write func(io.Writer) error) error {
	if err := safePath(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("Не удалось создать папку результата.")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("Не удалось создать новый файл. Проверьте, что он ещё не существует и папка доступна.")
	}
	ok := false
	defer func() {
		file.Close()
		if !ok {
			os.Remove(path)
		}
	}()
	if err := write(file); err != nil {
		return errors.New("Не удалось полностью записать результат.")
	}
	if err := file.Sync(); err != nil {
		return errors.New("Не удалось сохранить результат на диск.")
	}
	if err := file.Close(); err != nil {
		return errors.New("Не удалось закрыть файл результата.")
	}
	ok = true
	return nil
}

func generate(writer io.Writer, rows int64) error {
	buffer := bufio.NewWriter(writer)
	names := [...]string{"api", "worker", "gateway"}
	for i := int64(0); i < rows; i++ {
		status := 200
		if i%20 == 0 {
			status = 503
		} else if i%25 == 0 {
			status = 404
		}
		if _, err := fmt.Fprintf(buffer, "{\"service\":%q,\"status\":%d,\"duration_ms\":%d}\n", names[i%3], status, i%1000+1); err != nil {
			return err
		}
	}
	for _, line := range []string{
		`{`, `{"service":"api","status":200,"duration_ms":10,"extra":1}`,
		`{"service":"bad service","status":200,"duration_ms":10}`,
		`{"service":"api","status":99,"duration_ms":10}`,
		`{"service":"api","status":200,"duration_ms":70000}`,
	} {
		if _, err := fmt.Fprintln(buffer, line); err != nil {
			return err
		}
	}
	return buffer.Flush()
}

func execute(args []string, out, errOut io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(errOut, "Ошибка:", err); return 1 }
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(out, usage)
		return 0
	}
	command := args[0]
	if command != "demo" && command != "run" {
		return fail(errors.New("Неизвестная команда. Справка: help."))
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	asJSON := flags.Bool("json", false, "результат JSON")
	var input, output *string
	var rows *int64
	var maxServices *int
	var slow *int64
	if command == "demo" {
		output = flags.String("output", "workspace/requests.jsonl", "новый файл демо")
		rows = flags.Int64("rows", 10000, "корректных строк от 1 до 5000000")
	} else {
		input = flags.String("input", "workspace/requests.jsonl", "файл JSONL")
		output = flags.String("output", "", "новый файл результата JSON")
		maxServices = flags.Int("max-services", 64, "сервисов от 1 до 1024")
		slow = flags.Int64("slow-ms", 500, "порог от 0 до 60000 мс")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(out, usage)
			return 0
		}
		return fail(errors.New("Неверные параметры. Справка: help."))
	}
	if flags.NArg() != 0 {
		return fail(errors.New("Неожиданные аргументы. Справка: help."))
	}
	if command == "demo" {
		if *rows < 1 || *rows > 5_000_000 {
			return fail(errors.New("Для демо нужно от 1 до 5000000 корректных строк."))
		}
		if err := writeExclusive(*output, func(w io.Writer) error { return generate(w, *rows) }); err != nil {
			return fail(err)
		}
		if *asJSON {
			if err := json.NewEncoder(out).Encode(map[string]int64{"accepted_rows": *rows, "invalid_rows": 5, "input_rows": *rows + 5}); err != nil {
				return fail(errors.New("Не удалось вывести результат."))
			}
		} else {
			fmt.Fprintf(out, "Создано корректных строк: %d\nДобавлено ошибочных строк: 5\n", *rows)
		}
		return 0
	}
	if err := safePath(*input); err != nil {
		return fail(err)
	}
	file, err := os.Open(*input)
	if err != nil {
		return fail(errors.New("Не удалось открыть входной файл."))
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fail(errors.New("Вход должен быть обычным файлом."))
	}
	options := metrics.DefaultOptions()
	options.MaxServices = *maxServices
	options.SlowMS = *slow
	summary, err := metrics.Aggregate(file, options)
	if err != nil {
		return fail(err)
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fail(errors.New("Не удалось сформировать JSON."))
	}
	data = append(data, '\n')
	if *output != "" {
		if err := writeExclusive(*output, func(w io.Writer) error { _, err := w.Write(data); return err }); err != nil {
			return fail(err)
		}
	}
	if *asJSON {
		if _, err := out.Write(data); err != nil {
			return fail(errors.New("Не удалось вывести результат."))
		}
	} else {
		fmt.Fprint(out, summary.Russian())
	}
	return 0
}

func main() { os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr)) }
