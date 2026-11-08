#!/bin/sh
# Builds hkswitches for the Pi Zero W and upgrades it in place over ssh.
# Settings, switch states and HomeKit pairing in /var/lib/hkswitches are kept.
# Usage: ./deploy.sh user@pi-host
set -eu
HOST=${1:?usage: ./deploy.sh user@pi-host}
cd "$(dirname "$0")"
command -v go >/dev/null || { echo "Go is not installed (brew install go)"; exit 1; }

echo "Building for linux/arm (ARMv6)..."
(cd source && CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 \
  go build -trimpath -ldflags "-s -w" -o ../hkswitches .)

# One shared ssh connection, so you're asked for an ssh password at most once.
SSH_OPTS="-o ControlMaster=auto -o ControlPath=/tmp/hks-ssh-%C -o ControlPersist=60"

DIR=$(ssh $SSH_OPTS "$HOST" 'mktemp -d /tmp/hkswitches-deploy.XXXXXX')
echo "Copying to $HOST:$DIR ..."
scp -q $SSH_OPTS hkswitches hkswitches.service install.sh "$HOST:$DIR/"

# -t gives sudo a terminal to ask for your password if it needs one.
ssh -t $SSH_OPTS "$HOST" "sudo sh $DIR/install.sh; rc=\$?; rm -rf $DIR; exit \$rc"
