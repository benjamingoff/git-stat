#!/bin/sh
# Quick launcher for git-stat. Usage: ./run.sh [directory]
exec go run ./cmd/app "$@"
