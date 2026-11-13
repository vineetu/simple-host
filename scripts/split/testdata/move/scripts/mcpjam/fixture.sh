#!/usr/bin/env bash
cat <"$here/../../db/schema.sql"
cat <"$(dirname "$0")/../../db/schema.sql"
