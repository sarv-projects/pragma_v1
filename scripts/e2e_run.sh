#!/usr/bin/env bash
set -e

echo "🚀 Building Pragma..."
go build -o pragma ./cmd/pragma

echo "📦 Generating Test Manifest..."
cat <<EOF > test_manifest.json
{
  "description": "A very simple FastAPI app that has one GET endpoint returning 'Hello World'.",
  "project_name": "hello_fastapi"
}
EOF

echo "🧪 Running Pragma End-to-End (Headless Mode)..."
# Setting budget to something small to prevent infinite looping
cat test_manifest.json | ./pragma --headless --budget 0.50

echo "✅ End-to-End run completed!"
echo "Check the 'output' folder for the generated project."
