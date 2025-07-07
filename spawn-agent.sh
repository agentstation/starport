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

# Task definitions - stored as JSON-like structure
declare -A TASKS=(
    ["P1-S1-1.1"]='
        name="Repository Initialization"
        workspace="starport-init"
        branch="task/P1-S1-1.1-repo-init"
        prereq=""
        prereq_verify=""
        architecture_sections="sections 1-3 for project overview"
        requirements="
- Create go.mod with module path: github.com/agentstation/starport
- Ensure .gitignore exists
- Create LICENSE file (MIT)
- Update README.md if needed"
    '
    ["P1-S1-1.2"]='
        name="Project Structure Setup"
        workspace="starport-structure"
        branch="task/P1-S1-1.2-project-structure"
        prereq="Task P1-S1-1.1 must be complete (check for go.mod)"
        prereq_verify="test -f go.mod || echo \"ERROR: go.mod not found - P1-S1-1.1 not complete\""
        architecture_sections="section 4 for directory structure"
        requirements="
- Create directory structure per ARCHITECTURE.md
- Create cmd/starport/main.go with CLI framework (urfave/cli)
- Implement \"version\" and \"serve\" commands
- Create Makefile with standard targets"
    '
    ["P1-S1-1.3"]='
        name="Development Environment"
        workspace="starport-devops"
        branch="task/P1-S1-1.3-dev-environment"
        prereq="Task P1-S1-1.2 must be complete (check for cmd/starport/main.go)"
        prereq_verify="test -f cmd/starport/main.go || echo \"ERROR: cmd/starport/main.go not found - P1-S1-1.2 not complete\""
        architecture_sections="DevOps sections"
        requirements="
- Create docker-compose.yml for local Valkey
- Set up GitHub Actions workflow (.github/workflows/ci.yml)
- Configure golangci-lint
- Add pre-commit hooks"
    '
    ["P1-S1-1.4"]='
        name="HTTP Server Foundation"
        workspace="starport-http"
        branch="task/P1-S1-1.4-http-server"
        prereq="Task P1-S1-1.2 must be complete"
        prereq_verify="test -f cmd/starport/main.go || echo \"ERROR: cmd/starport/main.go not found - P1-S1-1.2 not complete\""
        architecture_sections="HTTP server design"
        requirements="
- Implement HTTP server with chi router
- Add middleware: request ID, logging, recovery, CORS
- Create health check endpoints
- Implement graceful shutdown"
    '
    ["P1-S1-1.5"]='
        name="Configuration System"
        workspace="starport-config"
        branch="task/P1-S1-1.5-config-system"
        prereq="Task P1-S1-1.4 must be complete"
        prereq_verify="test -f internal/server/server.go || echo \"ERROR: internal/server/server.go not found - P1-S1-1.4 not complete\""
        architecture_sections="configuration design"
        requirements="
- Define configuration structures
- Implement YAML loading with viper
- Add environment variable mapping
- Create validation logic
- Implement hot reload for rate limits"
    '
    ["P1-S2-2.1"]='
        name="Storage Interface Definition"
        workspace="starport-storage-interface"
        branch="task/P1-S2-2.1-storage-interface"
        prereq="Task P1-S1-1.5 must be complete (configuration system)"
        prereq_verify="test -f internal/config/config.go || echo \"ERROR: internal/config/config.go not found - P1-S1-1.5 not complete\""
        architecture_sections="storage architecture sections"
        requirements="
- Define KVStore interface with all required operations
- Create error types for storage operations
- Add serialization helpers
- Create factory pattern for storage backends
- Add context support throughout
- Define transaction interface
- Create mock implementation for testing"
    '
    ["P1-S2-2.2"]='
        name="Badger DB Integration"
        workspace="starport-badger"
        branch="task/P1-S2-2.2-badger-integration"
        prereq="Task P1-S2-2.1 must be complete (storage interface)"
        prereq_verify="test -f internal/storage/interface.go || echo \"ERROR: internal/storage/interface.go not found - P1-S2-2.1 not complete\""
        architecture_sections="Badger configuration"
        requirements="
- Implement BadgerStore struct
- Configure Badger options for performance
- Implement all KVStore interface methods
- Add TTL support for rate limiting
- Create backup/restore utilities
- Add compaction scheduling
- Write comprehensive tests"
    '
    ["P1-S2-2.3"]='
        name="Core Storage Models"
        workspace="starport-models"
        branch="task/P1-S2-2.3-storage-models"
        prereq="Task P1-S2-2.1 must be complete (storage interface)"
        prereq_verify="test -f internal/storage/interface.go || echo \"ERROR: internal/storage/interface.go not found - P1-S2-2.1 not complete\""
        architecture_sections="data models section"
        requirements="
- Define APIKey model with validation
- Define Preset model with versioning
- Define BYOKCredential with encryption
- Create serialization helpers
- Add model validation
- Implement encryption/decryption
- Write model tests"
    '
    ["P1-S3-3.1"]='
        name="Model Connector Interface"
        workspace="starport-connector-interface"
        branch="task/P1-S3-3.1-connector-interface"
        prereq="Task P1-S1-1.4 must be complete (HTTP server)"
        prereq_verify="test -f internal/server/server.go || echo \"ERROR: internal/server/server.go not found - P1-S1-1.4 not complete\""
        architecture_sections="connector design"
        requirements="
- Define Connector interface
- Create request/response types
- Add streaming support
- Define provider config structure
- Create mock connector
- Add health check interface
- Write interface tests"
    '
    ["P1-S3-3.2"]='
        name="OpenAI & Anthropic Connectors"
        workspace="starport-connectors"
        branch="task/P1-S3-3.2-provider-connectors"
        prereq="Task P1-S3-3.1 must be complete (connector interface)"
        prereq_verify="test -f internal/connector/interface.go || echo \"ERROR: internal/connector/interface.go not found - P1-S3-3.1 not complete\""
        architecture_sections="provider implementations"
        requirements="
- Implement OpenAI connector
- Add OpenAI streaming support
- Implement Anthropic connector
- Add Anthropic streaming
- Configure connection pooling
- Add retry logic
- Write integration tests"
    '
    ["P1-S3-3.3"]='
        name="Proxy Endpoints Implementation"
        workspace="starport-proxy"
        branch="task/P1-S3-3.3-proxy-endpoints"
        prereq="Task P1-S3-3.1 must be complete (connector interface)"
        prereq_verify="test -f internal/connector/interface.go || echo \"ERROR: internal/connector/interface.go not found - P1-S3-3.1 not complete\""
        architecture_sections="API endpoints"
        requirements="
- Implement /v1/chat/completions
- Add streaming for chat endpoint
- Implement /v1/embeddings
- Implement /v1/models
- Add OpenRouter endpoints
- Create request validators
- Add response transformers
- Write endpoint tests"
    '
    ["P1-S3-3.4"]='
        name="Advanced Routing System"
        workspace="starport-routing"
        branch="task/P1-S3-3.4-routing-system"
        prereq="Task P1-S3-3.2 must be complete (provider connectors)"
        prereq_verify="test -d internal/connector/openai || echo \"ERROR: OpenAI connector not found - P1-S3-3.2 not complete\""
        architecture_sections="routing strategies"
        requirements="
- Implement routing interface
- Add latency tracking (EMA)
- Implement cost-based routing
- Add content classifier
- Create fallback logic
- Add circuit breakers
- Implement health checks
- Write routing tests"
    '
    ["P1-S4-4.1"]='
        name="BYOK Implementation"
        workspace="starport-byok"
        branch="task/P1-S4-4.1-byok-implementation"
        prereq="Task P1-S2-2.3 must be complete (storage models)"
        prereq_verify="test -f internal/models/apikey.go || echo \"ERROR: API key model not found - P1-S2-2.3 not complete\""
        architecture_sections="BYOK security design"
        requirements="
- Implement encryption layer
- Add key derivation (Argon2)
- Create BYOK manager
- Add provider key mapping
- Implement key rotation
- Add audit logging
- Write security tests"
    '
    ["P1-S4-4.2"]='
        name="Caching System"
        workspace="starport-cache"
        branch="task/P1-S4-4.2-caching-system"
        prereq="Task P1-S3-3.3 must be complete (proxy endpoints)"
        prereq_verify="test -f internal/proxy/handlers.go || echo \"ERROR: Proxy handlers not found - P1-S3-3.3 not complete\""
        architecture_sections="caching architecture"
        requirements="
- Integrate Ristretto cache
- Implement cache key generation
- Add KV store cache layer
- Create cache policies
- Add invalidation logic
- Implement cache warming
- Write cache tests"
    '
    ["P1-S4-4.3"]='
        name="Content Filtering Pipeline"
        workspace="starport-filters"
        branch="task/P1-S4-4.3-content-filtering"
        prereq="Task P1-S3-3.3 must be complete (proxy endpoints)"
        prereq_verify="test -f internal/proxy/handlers.go || echo \"ERROR: Proxy handlers not found - P1-S3-3.3 not complete\""
        architecture_sections="filtering design"
        requirements="
- Create filter interface
- Implement pre-request filters
- Add post-response filters
- Create PII detector
- Add regex filters
- Build filter chains
- Write filter tests"
    '
    ["P1-S4-4.4"]='
        name="Preset Management System"
        workspace="starport-presets"
        branch="task/P1-S4-4.4-preset-management"
        prereq="Task P1-S2-2.3 must be complete (storage models)"
        prereq_verify="test -f internal/models/preset.go || echo \"ERROR: Preset model not found - P1-S2-2.3 not complete\""
        architecture_sections="preset management"
        requirements="
- Create preset manager
- Add template variable support
- Implement inheritance
- Add version control
- Create CRUD operations
- Add validation
- Write preset tests"
    '
)

# Common workflow template
WORKFLOW_TEMPLATE="IMPORTANT WORKFLOW:
1. IMMEDIATELY update TASKS.md to mark your task as 'In Progress'
   - Add entry to 'Active Work' table with your task, branch, and status
2. Complete the task requirements below
3. Update TASKS.md when PR is ready:
   - Move task to 'Recently Completed' section
   - Add PR number
4. Create and submit the PR

Note: TASKS.md is the single source of truth for task status!"

# Function to generate agent context
generate_context() {
    local task_id=$1
    local task_info=$2
    
    # Parse task info
    eval "$task_info"
    
    # Build file list
    local files_to_read="1. \$REPO_PATH/CLAUDE.md - Understand the workflow
2. \$REPO_PATH/TASKS.md - Find task $task_id for detailed requirements
3. \$REPO_PATH/ARCHITECTURE.md - Review $architecture_sections"
    
    # Add optional files based on task
    if [[ -n "$additional_files" ]]; then
        files_to_read="$files_to_read
$additional_files"
    fi
    
    echo "You are working on task $task_id ($name) for the Starport project.

Project: Starport - A high-performance LLM gateway
${prereq:+Prerequisite: $prereq
}${prereq_verify:+Verification command: $prereq_verify
}Workspace: \$REPO_PATH

$WORKFLOW_TEMPLATE

First, read these files:
$files_to_read

Your task requirements:$requirements

Additional instructions:
- Create branch: $branch
- Follow PR format in TASKS.md
- Check acceptance criteria in TASKS.md for this task
- Ensure all tests pass before submitting PR"
}

# Function to setup workspace
setup_workspace() {
    local task_id=$1
    local workspace_name=$2
    
    local workspace_path="$WORKSPACE_ROOT/$workspace_name"
    
    # Check if workspace already exists
    if [ -d "$workspace_path" ]; then
        echo -e "${YELLOW}Workspace already exists at: $workspace_path${NC}"
        echo ""
        echo "What would you like to do?"
        echo "1) Use existing workspace (pull latest changes)"
        echo "2) Remove and recreate workspace"
        echo "3) Cancel"
        echo ""
        read -p "Choice (1-3): " choice
        
        case $choice in
            1)
                cd "$workspace_path"
                echo -e "${BLUE}Pulling latest changes...${NC}"
                git checkout main
                git pull origin main
                ;;
            2)
                echo -e "${RED}Removing existing workspace...${NC}"
                rm -rf "$workspace_path"
                echo -e "${BLUE}Cloning fresh workspace...${NC}"
                git clone "$REPO_URL" "$workspace_path"
                cd "$workspace_path"
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
        echo -e "${BLUE}Creating workspace directory...${NC}"
        mkdir -p "$WORKSPACE_ROOT"
        echo -e "${BLUE}Cloning new workspace...${NC}"
        git clone "$REPO_URL" "$workspace_path"
        cd "$workspace_path"
    fi
    
    echo "$workspace_path"
}

# Main script
if [ -z "$1" ]; then
    echo "Usage: ./spawn-agent.sh <TASK-ID>"
    echo ""
    echo "This script will:"
    echo "  1. Create a separate clone for the agent (avoids Git conflicts)"
    echo "  2. Provide the complete context for the task"
    echo "  3. Start Claude Code in the workspace"
    echo ""
    echo "Available Phase 1 Tasks:"
    echo ""
    
    # List tasks in order
    for task_id in P1-S1-1.1 P1-S1-1.2 P1-S1-1.3 P1-S1-1.4 P1-S1-1.5 \
                   P1-S2-2.1 P1-S2-2.2 P1-S2-2.3 \
                   P1-S3-3.1 P1-S3-3.2 P1-S3-3.3 P1-S3-3.4 \
                   P1-S4-4.1 P1-S4-4.2 P1-S4-4.3 P1-S4-4.4; do
        if [[ -n "${TASKS[$task_id]}" ]]; then
            eval "${TASKS[$task_id]}"
            echo "  $task_id - $name"
        fi
    done
    exit 1
fi

TASK_ID=$1

# Check if task exists
if [[ -z "${TASKS[$TASK_ID]}" ]]; then
    echo -e "${RED}Error: Unknown task ID: $TASK_ID${NC}"
    echo "Run without arguments to see available tasks."
    exit 1
fi

# Get task info
eval "${TASKS[$TASK_ID]}"

echo -e "${GREEN}=== Spawning Agent for Task $TASK_ID ===${NC}"
echo -e "Task: ${BLUE}$name${NC}"
echo -e "Workspace: ${BLUE}$workspace${NC}"
echo ""

# Set up workspace
WORKSPACE_PATH=$(setup_workspace "$TASK_ID" "$workspace")
cd "$WORKSPACE_PATH"

# Run prerequisite verification if provided
if [[ -n "$prereq_verify" ]]; then
    echo -e "${YELLOW}Verifying prerequisites...${NC}"
    eval "$prereq_verify"
    if [ $? -ne 0 ]; then
        echo -e "${RED}Prerequisites not met. Please complete the dependent task first.${NC}"
        exit 1
    fi
    echo -e "${GREEN}Prerequisites verified!${NC}"
    echo ""
fi

# Generate context
REPO_PATH=$(pwd)
AGENT_CONTEXT=$(generate_context "$TASK_ID" "${TASKS[$TASK_ID]}")

# Save context to file
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
    echo -e "${YELLOW}Starting Claude Code with context...${NC}"
    echo ""
    # Start Claude Code with the context file
    cd "$WORKSPACE_PATH"
    claude --yolo "$CONTEXT_FILE"
else
    echo -e "${GREEN}=== Context Generated ===${NC}"
    echo ""
    echo "Copy and paste this context into Claude Code:"
    echo ""
    echo "----------------------------------------"
    cat "$CONTEXT_FILE"
    echo "----------------------------------------"
    echo ""
    echo -e "${YELLOW}Note: Install Claude CLI for automatic startup${NC}"
    echo "Visit: https://claude.ai/cli"
fi