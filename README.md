# Fiscatoni

A self-hosted Go importer for USD card transactions, backed by MySQL 8.4. This first version runs from the command line. Previewing a file requires only the importer image.

## Preview a bank export

From the project directory in PowerShell, build the image and mount one CSV read-only. The filename inside the container can be anything. Recognition uses the header.

```powershell
docker build -t fiscatoni:dev .
$csv = (Resolve-Path "$HOME/Downloads/chase.csv").Path
docker run --rm --network none --entrypoint fiscatoni --mount "type=bind,source=$csv,target=/input.csv,readonly" fiscatoni:dev preview /input.csv
```

Add `-rows` before `/input.csv` to print transaction details locally, including the physical CSV line number, transaction and posting dates, source category, and signed amount in cents. The default preview prints only the recognized layout and row count.

| Export | Interpretation |
| --- | --- |
| Chase credit card | Uses the signed amount, US month/day/year dates, and bank category, transaction type and memo. |
| Capital One card | A debit is negative and a credit is positive. Preserves the category and card reference, including leading zeroes. |
| Robinhood card | A positive source amount becomes a negative ledger amount. Preserves merchant, description, source time, status and type. The export supplies no spending category. |

The importer keeps bank categories as supplied. A positive amount may be a refund or card payment. Spending reports and merchant categorization rules are future work. Robinhood brokerage activity uses a different layout.

## Save transactions

Create a local `.env` file in the project directory with a line of the form `MYSQL_PASSWORD=your-long-random-password`. Choose the password before the first database startup. Compose runs one container with MySQL and the Go executable. The importer connects over loopback. Network access is disabled and the named `ledger-data` volume retains the database between runs.

```powershell
docker compose up -d --build --wait
```

Choose a stable account label and reuse it on subsequent exports for that account. From PowerShell, use the following command to stream the original file bytes into the running container. This gives the importer access to just that file.

```powershell
cmd /c 'docker compose exec -T app fiscatoni import -account "My Chase card" - < "%USERPROFILE%\Downloads\chase.csv"'
```

The importer validates the entire file before saving. Each import commits as one database transaction. An identical file for the same bank and account returns its original batch ID. For overlapping exports, an existing row with the same activity date, signed amount, and normalized description triggers a review. Review the reported lines with `preview -rows`, then pass decisions such as `-skip-lines 4,8 -keep-lines 10` before the filename. An unresolved match saves nothing. Identical purchases within a single fresh export are retained. Edits to a date, description, or amount can prevent a match across files.

Stop the database with `docker compose down`. Keep its volume when replacing containers. The database password in `.env` is used to initialize a new volume.

## Map another layout

Use a local JSON profile when the headers are unfamiliar. Name the source columns and use a Go date layout (`2006-01-02` means year-month-day).

```json
{
  "Bank": "my-bank",
  "Date": "Activity Date",
  "Layout": "01/02/2006",
  "Description": "Details",
  "Amount": "Net Amount",
  "Category": "Spending Group"
}
```

Mount the profile alongside the CSV and supply it to preview or import.

```powershell
$profilePath = (Resolve-Path ./my-bank.json).Path
docker run --rm --network none --entrypoint fiscatoni -v "${csv}:/input.csv:ro" -v "${profilePath}:/profile.json:ro" fiscatoni:dev preview -mapping /profile.json /input.csv
```

For separate debit and credit columns, replace `Amount` with `Debit` and `Credit`. For a signed column that represents purchases as positive, set `"Invert": true`. Files may use comma, semicolon or tab delimiters and must be UTF-8, up to 32 MiB, with plain decimal USD amounts. The importer accepts reordered headers and an optional UTF-8 byte order mark.

## Go project layout

- `internal/importer` handles format recognition, CSV validation, and exact integer-cent conversion. It can run and be tested without MySQL.
- `internal/ledger` owns the MySQL tables, account-scoped duplicate checks, and atomic batch writes.
- `cmd/fiscatoni` connects the parser and ledger to the preview and import commands.

Run `go test ./...` with Go 1.26, or build the Dockerfile to run the same tests in its build stage. The runtime image contains MySQL and the compiled executable. Its build context excludes local CSV files and credentials.
