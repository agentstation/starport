#!/bin/bash
# Agent spawning script for Starport - handles workspace setup and starts Claude Code

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

# Configuration
REPO_URL="https://github.com/agentstation/starport"
WORKSPACE_ROOT="$HOME/starport-development"

# Common workflow template that applies to ALL tasks
WORKFLOW_TEMPLATE="IMPORTANT WORKFLOW:
1. IMMEDIATELY update COORDINATION.md to mark your task as 'In Progress'
2. Complete the task requirements
3. Update COORDINATION.md to mark as 'PR Submitted' with the PR number
4. Create and submit the PR"

# Function to generate agent context
generate_context() {
    local task_id=$1
    local task_name=$2
    local workspace=$3
    local branch=$4
    local prerequisite=$5
    local files_to_read=$6
    local requirements=$7
    
    echo "You are working on task $task_id ($task_name) for the Starport project.

Project: Starport - A high-performance LLM gateway
${prerequisite:+Prerequisite: $prerequisite
}Workspace: $workspace

$WORKFLOW_TEMPLATE

First, read these files:
$files_to_read

Your task requirements:
$requirements
- Create branch: $branch
- Follow PR format in TASKS.md"
}

# Check if task ID was provided
if [ -z "$1" ]; then
    echo "Usage: ./spawn-agent.sh <TASK-ID>"
    echo ""
    echo "This script will:"
    echo "  1. Create a separate clone for the agent (avoids Git conflicts)"
    echo "  2. Provide the complete context for the task"
    echo "  3. Start Claude Code in the workspace with --yolo mode"
    echo ""
    echo "Available first tasks:"
    echo "  P1-S1-1.1 - Repository Initialization (start here)"
    echo ""
    echo "After P1-S1-1.1 completes:"
    echo "  P1-S1-1.2 - Project Structure"
    echo ""
    echo "After P1-S1-1.5 completes:"
    echo "  P1-S2-2.1 - Storage Interface Definition"
    exit 1
fi

TASK_ID=$1

# Task definitions with workspace names
case $TASK_ID in
    "P1-S1-1.1")
        WORKSPACE_NAME="starport-init"
        AGENT_NAME="Foundation"
        TASK_NAME="Repository Initialization"
        BRANCH="task/P1-S1-1.1-repo-init"
        PREREQ=""
        FILES_TO_READ="1. \$REPO_PATH/CLAUDE.md - Understand the workflow
2. \$REPO_PATH/TASKS.md - Find task P1-S1-1.1 for detailed requirements
3. \$REPO_PATH/ARCHITECTURE.md - Review sections 1-3 for project overview
4. \$REPO_PATH/COORDINATION.md - Update your task status"
        REQUIREMENTS="- Create go.mod with module path: github.com/agentstation/starport
- Ensure .gitignore exists (already created)
- Create LICENSE file (MIT)
- Update README.md if needed"
        ;;
    "P1-S1-1.2")
        WORKSPACE_NAME="starport-structure"
        AGENT_NAME="Structure"
        TASK_NAME="Project Structure"
        BRANCH="task/P1-S1-1.2-project-structure"
        PREREQ="Task P1-S1-1.1 must be complete (check for go.mod)"
        FILES_TO_READ="1. \$REPO_PATH/CLAUDE.md - Understand the workflow
2. \$REPO_PATH/TASKS.md - Find task P1-S1-1.2 for requirements
3. \$REPO_PATH/ARCHITECTURE.md - Review section 4 for directory structure
4. \$REPO_PATH/COORDINATION.md - Update your task status"
        REQUIREMENTS="- Create directory structure per ARCHITECTURE.md
- Create cmd/starport/main.go with CLI framework (urfave/cli)
- Implement 'version' and 'serve' commands
- Create Makefile with standard targets"
        ;;
    "P1-S1-1.3")
        WORKSPACE_NAME="starport-devops"
        AGENT_NAME="DevOps"
        TASK_NAME="Development Environment"
        BRANCH="task/P1-S1-1.3-dev-environment"
        PREREQ="Task P1-S1-1.2 must be complete (check for cmd/starport/main.go)"
        FILES_TO_READ="1. \$REPO_PATH/TASKS.md - Find task P1-S1-1.3
2. \$REPO_PATH/ARCHITECTURE.md - Review DevOps sections
3. \$REPO_PATH/COORDINATION.md - Update your task status"
        REQUIREMENTS="- Create docker-compose.yml for local Valkey
- Set up GitHub Actions workflow (.github/workflows/ci.yml)
- Configure golangci-lint
- Add pre-commit hooks"
        ;;
    "P1-S1-1.4")
        WORKSPACE_NAME="starport-http"
        AGENT_NAME="HTTP"
        TASK_NAME="HTTP Server Foundation"
        BRANCH="task/P1-S1-1.4-http-server"
        PREREQ="Task P1-S1-1.2 must be complete"
        FILES_TO_READ="1. \$REPO_PATH/TASKS.md - Find task P1-S1-1.4
2. \$REPO_PATH/ARCHITECTURE.md - Review HTTP server design
3. \$REPO_PATH/COORDINATION.md - Update your task status"
        REQUIREMENTS="- Implement HTTP server with chi router
- Add middleware: request ID, logging, recovery, CORS
- Create health check endpoints
- Implement graceful shutdown"
        ;;
    "P1-S1-1.5")
        WORKSPACE_NAME="starport-config"
        AGENT_NAME="Config"
        TASK_NAME="Configuration System"
        BRANCH="task/P1-S1-1.5-config-system"
        PREREQ="Task P1-S1-1.4 must be complete"
        FILES_TO_READ="1. \$REPO_PATH/TASKS.md - Find task P1-S1-1.5
2. \$REPO_PATH/ARCHITECTURE.md - Review configuration design
3. \$REPO_PATH/COORDINATION.md - Update your task status"
        REQUIREMENTS="- Define configuration structures
- Implement YAML loading with viper
- Add environment variable mapping
- Create validation logic
- Implement hot reload for rate limits"
        ;;
    "P1-S2-2.1")
        WORKSPACE_NAME="starport-storage-interface"
        AGENT_NAME="Storage"
        TASK_NAME="Storage Interface Definition"
        BRANCH="task/P1-S2-2.1-storage-interface"
        PREREQ="Task P1-S1-1.5 must be complete (configuration system)"
        FILES_TO_READ="1. \$REPO_PATH/TASKS.md - Find task P1-S2-2.1
2. \$REPO_PATH/ARCHITECTURE.md - Review storage architecture sections
3. \$REPO_PATH/COORDINATION.md - Update your task status"
        REQUIREMENTS="- Define KVStore interface with all required operations
- Create error types for storage operations
- Add serialization helpers
- Create factory pattern for storage backends
- Add context support throughout
- Define transaction interface
- Create mock implementation for testing"
        ;;
    *)
        echo -e "${YELLOW}Unknown task ID: $TASK_ID${NC}"
        echo "Check TASKS.md for valid task IDs"
        exit 1
        ;;
esac

# Create workspace root if it doesn't exist
if [ ! -d "$WORKSPACE_ROOT" ]; then
    echo -e "${BLUE}Creating workspace root: $WORKSPACE_ROOT${NC}"
    mkdir -p "$WORKSPACE_ROOT"
fi

# Set up the workspace
WORKSPACE_PATH="$WORKSPACE_ROOT/$WORKSPACE_NAME"

echo -e "${GREEN}=== Setting Up Agent Workspace ===${NC}"
echo ""
echo "Task: $TASK_ID - $TASK_NAME"
echo "Workspace: $WORKSPACE_PATH"
echo ""

# Check if workspace already exists
if [ -d "$WORKSPACE_PATH" ]; then
    echo -e "${YELLOW}Workspace already exists!${NC}"
    echo ""
    echo "Options:"
    echo "1. Use existing workspace (pulls latest main)"
    echo "2. Remove and recreate workspace"
    echo "3. Cancel"
    echo ""
    read -p "Choose option (1-3): " choice
    
    case $choice in
        1)
            cd "$WORKSPACE_PATH"
            echo -e "${BLUE}Pulling latest changes...${NC}"
            git checkout main
            git pull origin main
            ;;
        2)
            echo -e "${RED}Removing existing workspace...${NC}"
            rm -rf "$WORKSPACE_PATH"
            echo -e "${BLUE}Cloning fresh workspace...${NC}"
            git clone "$REPO_URL" "$WORKSPACE_PATH"
            cd "$WORKSPACE_PATH"
            ;;
        3)
            echo "Cancelled."
            exit 0
            ;;
        *)
            echo "Invalid choice. Cancelled."
            exit 1
            ;;
    esac
else
    echo -e "${BLUE}Cloning new workspace...${NC}"
    git clone "$REPO_URL" "$WORKSPACE_PATH"
    cd "$WORKSPACE_PATH"
fi

# Get the full path for context
REPO_PATH=$(pwd)

# Generate context using the template function
AGENT_CONTEXT=$(generate_context "$TASK_ID" "$TASK_NAME" "$REPO_PATH" "$BRANCH" "$PREREQ" "$FILES_TO_READ" "$REQUIREMENTS")

# Save context to file for Claude Code
CONTEXT_FILE="$WORKSPACE_PATH/context-$TASK_ID.txt"
echo "$AGENT_CONTEXT" > "$CONTEXT_FILE"

echo -e "${GREEN}=== Workspace Ready ===${NC}"
echo ""
echo -e "Workspace location: ${BLUE}$WORKSPACE_PATH${NC}"
echo -e "Context saved to: ${BLUE}$CONTEXT_FILE${NC}"
echo ""

# Check if claude command exists
if command -v claude &> /dev/null; then
    echo -e "${GREEN}=== Starting Claude Code ===${NC}"
    echo ""
    echo -e "${YELLOW}Starting Claude Code with --yolo mode in workspace...${NC}"
    echo -e "${YELLOW}Context will be provided: Read context-$TASK_ID.txt${NC}"
    echo ""
    
    # Change to workspace directory and start Claude Code
    cd "$WORKSPACE_PATH"
    claude --yolo "Read context-$TASK_ID.txt to understand your task and begin work."
else
    echo -e "${YELLOW}Claude Code CLI not found. Manual steps:${NC}"
    echo ""
    echo "1. Change to workspace directory:"
    echo "   cd $WORKSPACE_PATH"
    echo ""
    echo "2. Start Claude Code with --yolo mode:"
    echo "   claude --yolo"
    echo ""
    echo "3. Tell Claude to read the context:"
    echo "   Read context-$TASK_ID.txt"
    echo ""
    echo -e "${GREEN}Or copy this context directly:${NC}"
    echo "----------------------------------------"
    echo "$AGENT_CONTEXT"
    echo "----------------------------------------"
fi