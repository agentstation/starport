#!/bin/bash
# Agent spawning script for Starport - follows Claude Code best practices

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Check if task ID was provided
if [ -z "$1" ]; then
    echo "Usage: ./spawn-agent.sh <TASK-ID>"
    echo ""
    echo "Available first tasks:"
    echo "  P1-S1-1.1 - Repository Initialization (start here)"
    echo ""
    echo "After P1-S1-1.1 completes:"
    echo "  P1-S1-1.2 - Project Structure"
    echo "  P1-S1-1.6 - OpenAPI Documentation"
    echo "  P1-S1-1.7 - Documentation Infrastructure"
    exit 1
fi

TASK_ID=$1

# Get current directory for full paths
REPO_PATH=$(pwd)

# Task definitions with complete context
case $TASK_ID in
    "P1-S1-1.1")
        AGENT_CONTEXT="You are working on task P1-S1-1.1 (Repository Initialization) for the Starport project.

Project: Starport - A high-performance LLM gateway (see README.md)
Your task: Initialize the repository with proper Go module structure

First, read these files in order:
1. $REPO_PATH/CLAUDE.md - Understand the workflow
2. $REPO_PATH/TASKS.md - Find task P1-S1-1.1 for detailed requirements
3. $REPO_PATH/ARCHITECTURE.md - Review sections 1-3 for project overview

Your task requirements:
- Create go.mod with module path: github.com/agentstation/starport
- Ensure .gitignore exists (already created)
- Create LICENSE file (MIT)
- Update README.md if needed
- Create branch: task/P1-S1-1.1-repo-init
- Follow PR format in TASKS.md

This is the first task with no dependencies. After completing, update COORDINATION.md."
        ;;
        
    "P1-S1-1.2")
        AGENT_CONTEXT="You are working on task P1-S1-1.2 (Project Structure) for the Starport project.

Project: Starport - A high-performance LLM gateway
Prerequisite: Task P1-S1-1.1 must be complete (check for go.mod)

First, read these files:
1. $REPO_PATH/CLAUDE.md - Understand the workflow
2. $REPO_PATH/TASKS.md - Find task P1-S1-1.2 for requirements
3. $REPO_PATH/ARCHITECTURE.md - Review section 4 for directory structure

Your task requirements:
- Create directory structure per ARCHITECTURE.md
- Create cmd/starport/main.go with CLI framework (urfave/cli)
- Implement 'version' and 'serve' commands
- Create Makefile with standard targets
- Create branch: task/P1-S1-1.2-project-structure

Other agents may be working on P1-S1-1.6 and P1-S1-1.7 in parallel."
        ;;
        
    "P1-S1-1.3")
        AGENT_CONTEXT="You are working on task P1-S1-1.3 (Development Environment) for the Starport project.

Prerequisite: Task P1-S1-1.2 must be complete (check for cmd/starport/main.go)

Read these files:
1. $REPO_PATH/TASKS.md - Find task P1-S1-1.3
2. $REPO_PATH/ARCHITECTURE.md - Review DevOps sections

Your task:
- Create docker-compose.yml for local Valkey
- Set up GitHub Actions workflow (.github/workflows/ci.yml)
- Configure golangci-lint
- Add pre-commit hooks
- Create branch: task/P1-S1-1.3-dev-environment"
        ;;
        
    "P1-S1-1.4")
        AGENT_CONTEXT="You are working on task P1-S1-1.4 (HTTP Server Foundation) for the Starport project.

Prerequisite: Task P1-S1-1.2 must be complete

Read:
1. $REPO_PATH/TASKS.md - Find task P1-S1-1.4
2. $REPO_PATH/ARCHITECTURE.md - Review HTTP server design

Your task:
- Implement HTTP server with chi router
- Add middleware: request ID, logging, recovery, CORS
- Create health check endpoints
- Implement graceful shutdown
- Create branch: task/P1-S1-1.4-http-server"
        ;;
        
    "P1-S1-1.6")
        AGENT_CONTEXT="You are working on task P1-S1-1.6 (OpenAPI Documentation) for the Starport project.

Prerequisite: Task P1-S1-1.1 must be complete
Note: You can work in parallel with tasks P1-S1-1.2 and P1-S1-1.7

Read:
1. $REPO_PATH/TASKS.md - Find task P1-S1-1.6
2. $REPO_PATH/ARCHITECTURE.md - Review API design sections

Your task:
- Create docs/openapi/ directory
- Create OpenAPI 3.1 specification
- Document all planned endpoints
- Follow OpenAI/OpenRouter API patterns
- Create branch: task/P1-S1-1.6-openapi"
        ;;
        
    "P1-S1-1.7")
        AGENT_CONTEXT="You are working on task P1-S1-1.7 (Documentation Infrastructure) for the Starport project.

Prerequisite: Task P1-S1-1.1 must be complete
Note: You can work in parallel with tasks P1-S1-1.2 and P1-S1-1.6

Read:
1. $REPO_PATH/TASKS.md - Find task P1-S1-1.7
2. Review existing docs structure

Your task:
- Set up documentation system (MkDocs or similar)
- Create docs/ directory structure
- Set up documentation build process
- Create branch: task/P1-S1-1.7-docs-infra"
        ;;
        
    *)
        echo -e "${YELLOW}Unknown task ID: $TASK_ID${NC}"
        echo "Check TASKS.md for valid task IDs"
        exit 1
        ;;
esac

echo -e "${GREEN}=== Claude Code Agent Context ===${NC}"
echo ""
echo -e "${BLUE}Copy this entire context when starting Claude Code:${NC}"
echo ""
echo "----------------------------------------"
echo "$AGENT_CONTEXT"
echo "----------------------------------------"
echo ""
echo -e "${GREEN}Alternative: Save to file and reference:${NC}"
echo "echo '$AGENT_CONTEXT' > context-$TASK_ID.txt"
echo "Then tell Claude: 'Read context-$TASK_ID.txt to understand your task'"
echo ""