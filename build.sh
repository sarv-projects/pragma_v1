#!/bin/bash
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"

nvm install 20
nvm use 20

cd ~/projects/pragma/web
npm install
npm run build

cd ~/projects/pragma
go build -o pragma ./cmd/pragma
