#!/usr/bin/env bash
# deploy.sh — Deploy Slacker to r3x.io
# Run as root on the server: bash deploy.sh
set -euo pipefail

r3x-deploy-go-repo slacker slacker.r3x.io git@github.com:ev-claw/slacker.git main
