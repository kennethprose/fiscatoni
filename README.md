# Fiscatoni

A self-hosted Go importer for USD card transactions, backed by MySQL 8.4. The browser interface and CLI use the same parser and database.

## Browse transactions

Open the home page to view imported transactions, newest first. Filter by account, category, activity date, or money in and out. Search matches the merchant and description. Date filters include both selected days. **Uncategorized** shows transactions with an empty category.

Use **Edit category** on a saved row to choose an existing category, add a new one, or clear the category. The edit applies to that transaction. Use **Delete** and confirm to remove a row. If you import the same file again into the same account, the importer reviews the deleted rows while preserving the other rows and previous skip decisions.

## Import in the browser

Set `MYSQL_PASSWORD` in a local `.env` file before first startup, then run `docker compose up -d --build --wait`. Open [Fiscatoni](http://127.0.0.1:8080) and choose **Imports**. To choose another host port, set `FISCATONI_PORT` in the same file.

Open **Accounts** to save a bank and account name to your local profile. Accounts created by previous imports appear here too.

Drop a CSV or choose a file. A known bank layout opens a transaction preview. Use **Edit column mapping** to adjust the interpretation, or map an unfamiliar file using its headers and sample values. Use **Update preview** after changing a mapping. Choose a **Destination account** for that bank, or use **Add account** during the import. When the bank has one saved account, the importer selects it automatically.

Review each signed USD amount and category. The category menu includes categories from previous imports and the current statement. Choose **Add a new category…** to enter a name and reuse it on other rows in the preview. Categories become available for later imports when you save a transaction using them.

Check **Remember for merchant** to use that category on future imports across your accounts. Matching uses the merchant field, or the full description when the export omits a merchant. It ignores case and differences in whitespace. A remembered category takes precedence over the bank category. You can change a row for this import only, or check the box again to update the rule.

Select **Save to MySQL** to commit the import. If existing transactions match, choose **Keep** or **Skip** for each flagged row and save again. Only rows added to the ledger can create or update a merchant rule.

The browser sends the selected CSV only to the local Go server. Previews are processed in memory. The container serves all interface assets locally, publishes the web port only on host loopback, and keeps MySQL on container loopback.

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

The offline CLI preview shows the bank's category. CLI imports apply saved merchant rules when writing to MySQL. A positive amount may be a refund or card payment. Robinhood brokerage activity uses a different layout.

## Save transactions

The browser and CLI share a single container and the persistent `ledger-data` volume. Start the container before a CLI import.

```powershell
docker compose up -d --build --wait
```

Choose a stable account label and reuse it on subsequent exports for that account. From PowerShell, use the following command to stream the original file bytes into the running container. This gives the importer access to just that file.

```powershell
cmd /c 'docker compose exec -T app fiscatoni import -account "My Chase card" - < "%USERPROFILE%\Downloads\chase.csv"'
```

The importer validates the entire file before saving. Each import commits as one database transaction. An identical file for the same bank and account returns its original batch ID and can replace previously deleted rows. For overlapping exports, an existing row with the same activity date, signed amount, and normalized description triggers a review. Review the reported lines with `preview -rows`, then pass decisions such as `-skip-lines 4,8 -keep-lines 10` before the filename. An unresolved match saves nothing. Identical purchases within a single fresh export are retained. Edits to a date, description, or amount can prevent a match across files.

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
