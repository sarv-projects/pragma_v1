#!/bin/bash
kill -9 $(lsof -t -i :3777)
cp ~/projects/pragma/pragma ~/.local/bin/pragma
nohup ~/.local/bin/pragma > /dev/null 2>&1 &
