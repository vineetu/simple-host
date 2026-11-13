#!/bin/sh
cd "$(dirname "$0")/.."
cat "$PWD/internal/alpha/data.txt"
cat db/schema.sql
