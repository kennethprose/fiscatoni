# Fiscatoni

Fiscatoni is a self-hosted app for tracking your spending. Import USD card transactions from bank CSV exports, then review your spending by month, account, or category in your browser. The app and its MySQL database run in one Docker container.

## Install

Fiscatoni runs on Linux/amd64. For Docker run or Compose, create a folder for Fiscatoni and save the following as `.env`, replacing the example password with one of your own. On [Unraid](#unraid), enter these settings in the web UI.

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

### Unraid

1. In **Docker → Add Container**, set **Name** to `fiscatoni`, **Repository** to `roseatoni/fiscatoni:latest`, and **Network Type** to `Bridge`.
2. Add a **Port** mapping from host port `8080` to container port `8080` (TCP). If the host port is in use, choose another free host port.
3. Add a **Path** mapping from `/mnt/user/appdata/fiscatoni` on the host to `/var/lib/mysql` in the container, with **Read/Write** access. This keeps your ledger in the `appdata` share across updates.
4. Add the four MySQL settings shown above as individual **Variable** entries, using your own password for `MYSQL_PASSWORD`. Apply the settings to start the container.

## Open Fiscatoni

Once the database finishes starting, open [Fiscatoni](http://127.0.0.1:8080) on the Docker host. For Unraid, open `http://<unraid-server-ip>:8080` from your trusted LAN, using the host port you chose. Select **Imports** to add your first CSV.

To use a hostname such as `tower.local` on Unraid, add a container variable named `FISCATONI_HOST` with that hostname as its value. Enter just the name, without the scheme or port.
