#!/bin/bash
set -e

echo "Setting up pre-commit hooks..."

# Check if pre-commit is installed
if ! command -v pre-commit &> /dev/null; then
    echo "pre-commit is not installed. Please install it first:"
    echo "  pip install pre-commit"
    echo "  or"
    echo "  brew install pre-commit"
    exit 1
fi

# Install the git hook scripts
pre-commit install
pre-commit install --hook-type commit-msg

echo "Pre-commit hooks installed successfully!"
echo ""
echo "To run pre-commit on all files manually:"
echo "  pre-commit run --all-files"