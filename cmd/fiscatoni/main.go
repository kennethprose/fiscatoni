package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"fiscatoni/internal/importer"
	"fiscatoni/internal/ledger"
	"fiscatoni/internal/web"
	"github.com/go-sql-driver/mysql"
)

const usage = `fiscatoni serve [-listen 127.0.0.1:8080]
fiscatoni preview [-rows] [-mapping profile.json] FILE
fiscatoni import -account LABEL [-mapping profile.json] [-keep-lines N,N] [-skip-lines N,N] FILE
Use - as FILE to read CSV bytes from standard input. Amounts are in USD cents.`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Println(usage)
		return nil
	}
	cmd := args[0]
	if cmd == "serve" {
		flags := flag.NewFlagSet(cmd, flag.ContinueOnError)
		listen := flags.String("listen", "127.0.0.1:8080", "HTTP listen address")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		db, err := openDB()
		if err != nil {
			return err
		}
		defer db.Close()
		server := http.Server{Addr: *listen, Handler: web.Handler(db, os.Getenv("FISCATONI_HOST")), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 2 * time.Minute, IdleTimeout: time.Minute}
		return server.ListenAndServe()
	}
	if cmd != "import" && cmd != "preview" {
		return errors.New(usage)
	}
	flags := flag.NewFlagSet(cmd, flag.ContinueOnError)
	mapping := flags.String("mapping", "", "custom column profile (JSON)")
	var account, keep, skip string
	var rows bool
	if cmd == "import" {
		flags.StringVar(&account, "account", "", "stable label for the destination account")
		flags.StringVar(&keep, "keep-lines", "", "comma-separated overlapping lines to keep")
		flags.StringVar(&skip, "skip-lines", "", "comma-separated overlapping lines to skip")
	} else {
		flags.BoolVar(&rows, "rows", false, "show normalized rows, including private transaction details")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New(usage)
	}
	var profile *importer.Profile
	if *mapping != "" {
		data, err := os.ReadFile(*mapping)
		if err != nil {
			return err
		}
		profile = &importer.Profile{}
		d := json.NewDecoder(strings.NewReader(strings.TrimPrefix(string(data), "\ufeff")))
		d.DisallowUnknownFields()
		if err = d.Decode(profile); err != nil {
			return fmt.Errorf("mapping: %w", err)
		}
		if d.Decode(new(any)) != io.EOF {
			return errors.New("unexpected content after mapping")
		}
	}
	var input io.Reader = os.Stdin
	if path := flags.Arg(0); path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		input = f
	}
	const limit = 32 << 20
	data, err := io.ReadAll(io.LimitReader(input, limit+1))
	if err != nil {
		return err
	}
	if len(data) > limit {
		return errors.New("CSV exceeds 32 MiB")
	}
	b, err := importer.Parse(data, profile)
	if err != nil {
		return err
	}
	if cmd == "preview" {
		fmt.Printf("%s: %d rows\n", b.Bank, len(b.Rows))
		if rows {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			return encoder.Encode(b.Rows)
		}
		return nil
	}
	decisions := map[int]bool{}
	for i, list := range []string{keep, skip} {
		if list == "" {
			continue
		}
		for _, s := range strings.Split(list, ",") {
			line, err := strconv.Atoi(strings.TrimSpace(s))
			_, exists := decisions[line]
			if err != nil || line < 2 || exists {
				return errors.New("each line decision needs a distinct CSV line number >= 2")
			}
			decisions[line] = i == 0
		}
	}
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()
	result, err := ledger.Save(context.Background(), db, strings.TrimSpace(account), b, decisions)
	if err != nil {
		return err
	}
	if result.AlreadyImported {
		fmt.Printf("File already imported in batch %d.\n", result.ID)
		return nil
	}
	fmt.Printf("%s: batch %d saved, %d added, %d skipped.\n", b.Bank, result.ID, result.Added, result.Skipped)
	return nil
}

func openDB() (*sql.DB, error) {
	cfg := mysql.NewConfig()
	cfg.User = "fiscatoni"
	cfg.DBName = "fiscatoni"
	cfg.Net = "tcp"
	cfg.Addr = "127.0.0.1:3306"
	cfg.Passwd = os.Getenv("MYSQL_PASSWORD")
	cfg.Timeout = 10 * time.Second
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(connector), nil
}
