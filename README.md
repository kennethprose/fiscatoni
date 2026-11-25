# Fiscatoni

Fiscatoni is a self-hosted app for tracking your spending. Import USD card transactions from bank CSV exports, then review your spending by month, account, or category in your browser. The app and its MySQL database run in one Docker container.

## Install

On a machine that can run Linux/amd64 containers, create a folder for Fiscatoni. In that folder, save the following as `.env`, replacing the example password with one of your own. Then choose either installation method below.

```dotenv
MYSQL_DATABASE=fiscatoni
MYSQL_USER=fiscatoni
MYSQL_PASSWORD=replace-with-your-own-password
MYSQL_RANDOM_ROOT_PASSWORD=yes
```

### Docker run

Run this command from the folder containing `.env`.

```sh
docker run -d --name fiscatoni --restart unless-stopped --init \
  --env-file .env \
  -p 127.0.0.1:8080:8080 \
  -v fiscatoni_ledger-data:/var/lib/mysql \
  roseatoni/fiscatoni:latest
```

### Docker Compose

Save this as `compose.yaml` alongside `.env`.

```yaml
services:
  app:
    image: roseatoni/fiscatoni:latest
    restart: unless-stopped
    init: true
    env_file: .env
    ports:
      - "127.0.0.1:8080:8080"
    volumes:
      - ledger-data:/var/lib/mysql

volumes:
  ledger-data:
    name: fiscatoni_ledger-data
```

Start Fiscatoni from that folder with `docker compose up -d`.

## Open Fiscatoni

Once the database finishes starting, open [Fiscatoni](http://127.0.0.1:8080) on the Docker host and choose **Imports** to add your first CSV. Both installation methods store your transactions in the `fiscatoni_ledger-data` Docker volume, so they survive container replacement.
