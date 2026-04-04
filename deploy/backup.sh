#!/bin/bash
set -e

BACKUP_DIR="$HOME/backups/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$BACKUP_DIR"

echo "Backing up to $BACKUP_DIR..."

# Postgres dump
kubectl exec -n nolights statefulset/postgres -- \
  pg_dump -U postgres nolights > "$BACKUP_DIR/nolights.sql"
echo "  postgres -> nolights.sql"

echo "Done: $BACKUP_DIR"
ls -lh "$BACKUP_DIR"
