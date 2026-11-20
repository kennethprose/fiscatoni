#!/usr/bin/env bash
set -eu

docker-entrypoint.sh mysqld --bind-address=127.0.0.1 --mysqlx-bind-address=127.0.0.1 &
database_pid=$!
gosu mysql fiscatoni serve -listen :8080 &
web_pid=$!

stop_children() {
    kill -TERM "$web_pid" "$database_pid" 2>/dev/null || true
    wait "$web_pid" 2>/dev/null || true
    wait "$database_pid" 2>/dev/null || true
}
trap stop_children EXIT
trap 'exit 0' TERM INT
wait -n "$web_pid" "$database_pid"
