// Starport ChatUI JavaScript Client
(function () {
  "use strict";

  // Configuration
  const config = window.STARPORT_CONFIG || {
    apiBaseURL: window.location.origin,
    allowKeyGen: false,
  };

  // State
  const state = {
    currentChatId: null,
    chats: {},
    apiKey: localStorage.getItem("starport_api_key") || "",
    selectedModel: localStorage.getItem("starport_model") || "",
    streamEnabled: localStorage.getItem("starport_stream") !== "false",
    isGenerating: false,
    abortController: null,
    expandedReasoning: new Set(), // Track which messages have expanded reasoning
    autoScroll: true, // Track if auto-scroll is enabled
    userScrolled: false, // Track if user has manually scrolled
    pinnedChats: JSON.parse(
      localStorage.getItem("starport_pinned_chats") || "[]"
    ), // Track pinned chat IDs
  };

  // DOM Elements
  const elements = {
    // Header
    themeToggle: document.getElementById("theme-toggle"),
    settingsBtn: document.getElementById("settings-btn"),

    // Sidebar
    sidebar: document.getElementById("sidebar"),
    sidebarToggle: document.getElementById("sidebar-toggle"),
    drawerToggle: document.getElementById("drawer-toggle"),
    sidebarBackdrop: document.getElementById("sidebar-backdrop"),
    newChatBtn: document.getElementById("new-chat"),
    chatList: document.getElementById("chat-list"),
    clearAllBtn: document.getElementById("clear-all"),

    // Search
    searchBtn: document.getElementById("search-btn"),
    searchModal: document.getElementById("search-modal"),
    searchInput: document.getElementById("search-input"),
    searchResults: document.getElementById("search-results"),
    plusBtn: document.getElementById("plus-btn"),
    sidebarSearchInput: document.getElementById("sidebar-search-input"),

    // Chat
    modelSelect: document.getElementById("model-select"),
    modelPricing: document.getElementById("model-pricing"),
    messages: document.getElementById("messages"),
    messageInput: document.getElementById("message-input"),
    sendBtn: document.getElementById("send-btn"),
    stopBtn: document.getElementById("stop-btn"),
    tokenCount: document.getElementById("token-count"),
    costEstimate: document.getElementById("cost-estimate"),

    // Settings Modal
    settingsModal: document.getElementById("settings-modal"),
    apiKeyInput: document.getElementById("api-key"),
    apiBaseInput: document.getElementById("api-base"),
    streamEnabledInput: document.getElementById("stream-enabled"),
    generateKeyBtn: document.getElementById("generate-key"),

    // Error Toast
    errorToast: document.getElementById("error-toast"),
    toastMessage: document.querySelector(".toast-message"),
    toastDetails: document.querySelector(".toast-details"),
    errorJson: document.querySelector(".error-json"),
  };

  // Initialize
  function init() {
    loadChats();
    loadModels();
    setupEventListeners();
    setupScrollHandlers();
    updateUI();

    // Set initial sidebar state based on screen size
    const app = document.getElementById("app");

    if (window.innerWidth > 768) {
      // Desktop: sidebar is visible by default
      elements.sidebar.classList.add("active");
      app.classList.add("sidebar-open");
      elements.drawerToggle.setAttribute("aria-expanded", "true");
    }

    // Initialize Prism.js theme based on current theme
    const currentTheme = document.documentElement.getAttribute("data-theme");
    updatePrismTheme(currentTheme);

    // Configure Prism autoloader path for CDN languages
    if (
      typeof Prism !== "undefined" &&
      Prism.plugins &&
      Prism.plugins.autoloader
    ) {
      Prism.plugins.autoloader.languages_path =
        "https://cdn.jsdelivr.net/npm/prismjs@1.29.0/components/";
    }

    // Initialize Mermaid with theme-aware configuration
    if (typeof mermaid !== "undefined") {
      mermaid.initialize({
        startOnLoad: false,
        theme: currentTheme === "dark" ? "dark" : "default",
        securityLevel: "strict",
        flowchart: {
          useMaxWidth: true,
          htmlLabels: true,
          curve: "basis",
        },
        sequence: {
          useMaxWidth: true,
          showSequenceNumbers: true,
        },
        gantt: {
          numberSectionStyles: 4,
          leftPadding: 120,
        },
      });
    }

    // Set up lazy loading for diagrams
    if ("IntersectionObserver" in window) {
      const diagramObserver = new IntersectionObserver(
        (entries) => {
          entries.forEach((entry) => {
            if (entry.isIntersecting) {
              const container = entry.target;
              const diagramData = container.getAttribute(
                "data-mermaid-diagram"
              );
              if (diagramData && typeof mermaid !== "undefined") {
                renderMermaidDiagram(container, diagramData);
                diagramObserver.unobserve(container);
              }
            }
          });
        },
        {
          root: null,
          rootMargin: "100px",
        }
      );

      // Store observer for later use
      state.diagramObserver = diagramObserver;
    }

    // Create new chat if no chats exist
    if (Object.keys(state.chats).length === 0) {
      createNewChat();
    } else {
      // Load the most recent chat
      const chatIds = Object.keys(state.chats).sort(
        (a, b) => state.chats[b].lastModified - state.chats[a].lastModified
      );
      loadChat(chatIds[0]);
    }
  }

  // Event Listeners
  function setupEventListeners() {
    // Theme toggle
    elements.themeToggle.addEventListener("click", toggleTheme);

    // Settings
    elements.settingsBtn.addEventListener("click", () =>
      showModal(elements.settingsModal)
    );
    elements.apiKeyInput.value = state.apiKey;
    elements.streamEnabledInput.checked = state.streamEnabled;

    elements.apiKeyInput.addEventListener("change", (e) => {
      const previousKey = state.apiKey;
      state.apiKey = e.target.value;
      localStorage.setItem("starport_api_key", state.apiKey);
      updateUI();

      // Reload models when API key changes
      if (state.apiKey !== previousKey) {
        loadModels();
        // Show feedback
        if (state.apiKey) {
          showToast("API key saved successfully", "success");
        }
      }
    });

    elements.streamEnabledInput.addEventListener("change", (e) => {
      state.streamEnabled = e.target.checked;
      localStorage.setItem("starport_stream", state.streamEnabled);
    });

    if (elements.generateKeyBtn) {
      elements.generateKeyBtn.addEventListener("click", generateAPIKey);
    }

    // Modal close buttons
    document.querySelectorAll(".modal-close").forEach((btn) => {
      btn.addEventListener("click", () => hideModal(elements.settingsModal));
    });

    // Sidebar
    if (elements.sidebarToggle) {
      elements.sidebarToggle.addEventListener("click", toggleSidebar);
    }
    elements.drawerToggle.addEventListener("click", toggleSidebar);
    elements.sidebarBackdrop.addEventListener("click", closeSidebar);
    elements.newChatBtn.addEventListener("click", () => {
      const currentChat = state.chats[state.currentChatId];
      // If current chat is temporary and has no messages, just focus input
      if (
        currentChat &&
        currentChat.temporary &&
        currentChat.messages.length === 0
      ) {
        elements.messageInput.focus();
      } else {
        createNewChat();
      }
    });
    elements.clearAllBtn.addEventListener("click", clearAllChats);

    // Search
    elements.searchBtn.addEventListener("click", openSearch);
    elements.plusBtn.addEventListener("click", () => {
      const currentChat = state.chats[state.currentChatId];
      // If current chat is temporary and has no messages, just focus input
      if (
        currentChat &&
        currentChat.temporary &&
        currentChat.messages.length === 0
      ) {
        elements.messageInput.focus();
      } else {
        createNewChat();
      }
    });
    document
      .querySelector(".search-close")
      .addEventListener("click", closeSearch);
    elements.searchModal.addEventListener("click", (e) => {
      if (e.target === elements.searchModal) {
        closeSearch();
      }
    });
    elements.searchInput.addEventListener(
      "input",
      debounce(performSearch, 300)
    );
    elements.searchInput.addEventListener("keydown", handleSearchKeydown);

    // Sidebar search
    elements.sidebarSearchInput.addEventListener(
      "input",
      debounce(filterChatList, 150)
    );
    elements.sidebarSearchInput.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        elements.sidebarSearchInput.value = "";
        filterChatList();
        elements.sidebarSearchInput.blur();
      }
    });

    // Model selection
    elements.modelSelect.addEventListener("change", (e) => {
      state.selectedModel = e.target.value;
      localStorage.setItem("starport_model", state.selectedModel);
      updateUI();
      updateModelPricing();
      updateConversationStats();
    });

    // Message input
    elements.messageInput.addEventListener("input", () => {
      autoResizeTextarea(elements.messageInput);
      updateUI();
    });

    elements.messageInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        sendMessage();
      }
    });

    // Send/Stop buttons
    elements.sendBtn.addEventListener("click", sendMessage);
    elements.stopBtn.addEventListener("click", stopGeneration);

    // Error toast
    document.querySelector(".toast-close").addEventListener("click", hideError);
    elements.toastMessage.addEventListener("click", () => {
      elements.toastDetails.classList.toggle("hidden");
    });

    // Global keyboard shortcuts
    document.addEventListener("keydown", (e) => {
      // Ctrl/Cmd + K for search
      if ((e.ctrlKey || e.metaKey) && e.key === "k") {
        e.preventDefault();
        openSearch();
      }
      // Escape to close search
      if (
        e.key === "Escape" &&
        elements.searchModal.classList.contains("active")
      ) {
        closeSearch();
      }
    });

    // Handle window resize to sync sidebar state with breakpoint
    window.addEventListener(
      "resize",
      debounce(() => {
        const isMobile = window.innerWidth <= 768;
        const app = document.getElementById("app");

        if (isMobile) {
          // Mobile: close sidebar by default
          if (elements.sidebar.classList.contains("active")) {
            elements.sidebar.classList.remove("active");
            elements.sidebarBackdrop.classList.remove("active");
            app.classList.remove("sidebar-open");
            elements.modelSelect.parentElement.style.marginLeft = "100px";
          }
        } else {
          // Desktop: open sidebar by default
          if (!elements.sidebar.classList.contains("active")) {
            elements.sidebar.classList.add("active");
            app.classList.add("sidebar-open");
            elements.modelSelect.parentElement.style.marginLeft = "0";
          }
        }
      }, 150)
    );

    // Keyboard shortcuts
    document.addEventListener("keydown", handleKeyboardShortcuts);
  }

  function setupScrollHandlers() {
    let scrollTimeout;
    const scrollThreshold = 100; // pixels from bottom to consider "at bottom"

    // Create scroll to bottom button
    const scrollButton = document.createElement("button");
    scrollButton.className = "scroll-to-bottom";
    scrollButton.innerHTML = `
            <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="12" y1="5" x2="12" y2="19"></line>
                <polyline points="19 12 12 19 5 12"></polyline>
            </svg>
        `;
    scrollButton.style.opacity = "0";
    scrollButton.style.pointerEvents = "none";
    elements.messages.parentElement.appendChild(scrollButton);

    // Check if user is at bottom of messages
    function isAtBottom() {
      const { scrollTop, scrollHeight, clientHeight } = elements.messages;
      return scrollHeight - scrollTop - clientHeight < scrollThreshold;
    }

    // Update scroll button visibility
    function updateScrollButton() {
      if (isAtBottom()) {
        scrollButton.style.opacity = "0";
        scrollButton.style.pointerEvents = "none";
        state.autoScroll = true;
        state.userScrolled = false;
      } else {
        scrollButton.style.opacity = "1";
        scrollButton.style.pointerEvents = "auto";
      }
    }

    // Scroll to bottom smoothly
    function scrollToBottom(smooth = true) {
      elements.messages.scrollTo({
        top: elements.messages.scrollHeight,
        behavior: smooth ? "smooth" : "instant",
      });
      state.autoScroll = true;
      state.userScrolled = false;
    }

    // Handle scroll events
    elements.messages.addEventListener("scroll", () => {
      // IMMEDIATE actions (no delay)
      const atBottom = isAtBottom();

      // Show/hide button immediately with smooth transition
      if (atBottom) {
        scrollButton.style.opacity = "0";
        scrollButton.style.pointerEvents = "none";
      } else {
        scrollButton.style.opacity = "1";
        scrollButton.style.pointerEvents = "auto";
      }

      // If user scrolled up during streaming, disable auto-scroll IMMEDIATELY
      if (state.isGenerating && !atBottom) {
        state.userScrolled = true;
        state.autoScroll = false;
      }

      // If user scrolled to bottom (even during streaming), re-enable auto-scroll
      if (atBottom) {
        state.autoScroll = true;
        state.userScrolled = false;
      }

      // DEBOUNCED actions (with delay) - not needed anymore but kept for future use
      clearTimeout(scrollTimeout);
      scrollTimeout = setTimeout(() => {
        // Could add additional logic here if needed
      }, 150);
    });

    // Scroll to bottom button click
    scrollButton.addEventListener("click", () => {
      // Use instant scroll during streaming so we can keep up with new content
      scrollToBottom(!state.isGenerating);
    });

    // Store scroll functions globally for use in other parts
    window.scrollToBottom = scrollToBottom;
    window.isAtBottom = isAtBottom;
  }

  // Theme Management
  function toggleTheme() {
    const html = document.documentElement;
    const currentTheme = html.getAttribute("data-theme");
    const newTheme = currentTheme === "dark" ? "light" : "dark";
    html.setAttribute("data-theme", newTheme);
    localStorage.setItem("starport_theme", newTheme);

    // Update Prism.js theme
    updatePrismTheme(newTheme);

    // Update Mermaid theme
    if (typeof mermaid !== "undefined") {
      mermaid.initialize({
        theme: newTheme === "dark" ? "dark" : "default",
      });
    }
  }

  function updatePrismTheme(theme) {
    // Update Prism.js theme stylesheet
    const prismLink = document.getElementById("prism-theme");
    if (prismLink) {
      const baseUrl = "https://cdn.jsdelivr.net/npm/prismjs@1.29.0/themes/";
      const themeName = theme === "dark" ? "prism-tomorrow" : "prism";
      prismLink.href = `${baseUrl}${themeName}.min.css`;
    }
  }

  // Modal Management
  function showModal(modal) {
    modal.classList.add("active");
  }

  function hideModal(modal) {
    modal.classList.remove("active");
  }

  // Sidebar Management
  function toggleSidebar() {
    const app = document.getElementById("app");

    elements.sidebar.classList.toggle("active");
    elements.sidebarBackdrop.classList.toggle("active");

    // Toggle sidebar-open class on app
    if (elements.sidebar.classList.contains("active")) {
      app.classList.add("sidebar-open");
      elements.drawerToggle.setAttribute("aria-expanded", "true");
    } else {
      app.classList.remove("sidebar-open");
      elements.drawerToggle.setAttribute("aria-expanded", "false");
    }
  }

  function closeSidebar() {
    const app = document.getElementById("app");
    elements.sidebar.classList.remove("active");
    elements.sidebarBackdrop.classList.remove("active");
    app.classList.remove("sidebar-open");
    elements.drawerToggle.setAttribute("aria-expanded", "false");
  }

  // Chat Management
  function createNewChat() {
    const chatId = generateId();
    const chat = {
      id: chatId,
      title: "New Chat",
      messages: [],
      created: Date.now(),
      lastModified: Date.now(),
      temporary: true, // Mark as temporary until first message
    };

    state.chats[chatId] = chat;
    state.currentChatId = chatId;
    // Don't save temporary chats to localStorage or update chat list
    updateMessagesUI();
    updateConversationStats();
    elements.messageInput.focus();
  }

  function loadChat(chatId) {
    if (!state.chats[chatId]) return;

    state.currentChatId = chatId;
    updateChatList();
    updateMessagesUI();
    updateConversationStats();
  }

  function deleteChat(chatId) {
    delete state.chats[chatId];

    // Remove from pinned chats if it was pinned
    if (state.pinnedChats.includes(chatId)) {
      state.pinnedChats = state.pinnedChats.filter((id) => id !== chatId);
      localStorage.setItem(
        "starport_pinned_chats",
        JSON.stringify(state.pinnedChats)
      );
    }

    if (state.currentChatId === chatId) {
      const remainingChats = Object.keys(state.chats);
      if (remainingChats.length > 0) {
        loadChat(remainingChats[0]);
      } else {
        createNewChat();
      }
    }

    saveChats();
    updateChatList();

    // Update stats if this was the current chat
    if (state.currentChatId && state.chats[state.currentChatId]) {
      updateConversationStats();
    }
  }

  function renameChat(chatId) {
    const chat = state.chats[chatId];
    if (!chat) return;

    const newTitle = prompt("Enter new chat title:", chat.title);
    if (newTitle && newTitle.trim()) {
      chat.title = newTitle.trim();
      chat.lastModified = Date.now();
      saveChats();
      updateChatList();
    }
  }

  function togglePinChat(chatId) {
    const isPinned = state.pinnedChats.includes(chatId);

    if (isPinned) {
      // Unpin
      state.pinnedChats = state.pinnedChats.filter((id) => id !== chatId);
    } else {
      // Pin
      state.pinnedChats.push(chatId);
    }

    // Save pinned state
    localStorage.setItem(
      "starport_pinned_chats",
      JSON.stringify(state.pinnedChats)
    );
    updateChatList();
  }

  function clearAllChats() {
    if (
      confirm(
        "Are you sure you want to clear all chats? This cannot be undone."
      )
    ) {
      state.chats = {};
      state.pinnedChats = [];
      localStorage.removeItem("starport_chats");
      localStorage.removeItem("starport_pinned_chats");
      createNewChat();
    }
  }

  function updateChatTitle(chatId, firstMessage) {
    const chat = state.chats[chatId];
    if (chat && chat.messages.length === 1) {
      // Update title based on first message
      chat.title =
        firstMessage.substring(0, 50) + (firstMessage.length > 50 ? "..." : "");
      saveChats();
      updateChatList();
    }
  }

  async function generateChatTitle(chatId, userMessage) {
    try {
      const chat = state.chats[chatId];
      if (!chat) return;

      // Build context from existing messages if available
      let contextMessage = userMessage;
      if (chat.messages.length > 0) {
        // Include first user message and current message for better context
        const firstUserMsg = chat.messages.find(m => m.role === "user");
        if (firstUserMsg && firstUserMsg.content !== userMessage) {
          contextMessage = `First message: ${firstUserMsg.content}\n\nLatest message: ${userMessage}`;
        }
      }

      const response = await fetch(`${config.apiBaseURL}/v1/chat/completions`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${state.apiKey}`,
        },
        body: JSON.stringify({
          model: "google-aistudio/gemini-1.5-flash",
          messages: [
            {
              role: "system",
              content:
                "Generate a concise, descriptive title (max 6 words) for a chat based on the conversation. Respond with ONLY the title, no quotes or punctuation.",
            },
            {
              role: "user",
              content: contextMessage,
            },
          ],
          temperature: 0.7,
          max_tokens: 20,
        }),
      });

      if (!response.ok) {
        throw new Error("Failed to generate title");
      }

      const data = await response.json();
      const title = data.choices[0].message.content.trim();
      
      console.log("Generated title:", title); // Debug log

      // Update chat title if chat still exists
      if (chat && title) {
        chat.title = title;
        
        // Update the chat list immediately - before saving
        updateChatList();
        
        // Then save to localStorage
        saveChats();
        
        // If this is the current chat, update the page title too
        if (chatId === state.currentChatId) {
          document.title = `${title} - ${config.title || 'Starport LLM Chat'}`;
        }
      }
    } catch (error) {
      console.error("Error generating chat title:", error);
      // Don't remove the generating flag on error - let it stay until message generation completes
      // The title will just remain as the truncated first message
    }
  }

  // Message Generation
  async function generate(assistantMessage) {
    const chat = state.chats[state.currentChatId];
    if (!chat) return;

    // Start generation
    state.isGenerating = true;
    state.abortController = new AbortController();
    updateUI();

    try {
      const requestBody = {
        model: state.selectedModel,
        messages: chat.messages.slice(0, -1).map((m) => ({
          role: m.role,
          content: m.content,
        })),
        stream: state.streamEnabled,
      };

      // Add reasoning parameters for models that support it
      // Enable for Gemini 2.5 models (Pro, Flash, Flash-Lite)
      const model = state.selectedModel.toLowerCase();

      if (
        model.includes("gemini") &&
        (model.includes("2.5-pro") ||
          model.includes("2.5-flash") ||
          model.includes("2.5-flash-lite") ||
          model.includes("gemini-2.5-pro") ||
          model.includes("gemini-2.5-flash"))
      ) {
        requestBody.reasoning = {
          effort: "high", // Use dynamic thinking for best results
        };
      }

      const startTime = performance.now();

      const response = await fetch(`${config.apiBaseURL}/v1/chat/completions`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${state.apiKey}`,
        },
        body: JSON.stringify(requestBody),
        signal: state.abortController.signal,
      });

      if (!response.ok) {
        const error = await response.json();
        throw new Error(error.error?.message || `HTTP ${response.status}`);
      }

      if (state.streamEnabled) {
        await handleStreamingResponse(response, assistantMessage, startTime);
      } else {
        await handleNonStreamingResponse(response, assistantMessage, startTime);
      }
    } catch (error) {
      if (error.name === "AbortError") {
        assistantMessage.content += " [Generation stopped]";
        updateMessageDirectly(assistantMessage);
      } else {
        showError(error.message, error);
        chat.messages.pop(); // Remove failed assistant message
      }
    } finally {
      state.isGenerating = false;

      assistantMessage.streaming = false;

      // Clear generation flag for current chat
      const chat = state.chats[state.currentChatId];
      if (chat && chat.isGenerating) {
        chat.isGenerating = false;
        updateChatList();
      }

      saveChats();
      updateUI();
      // Don't rebuild the entire UI, just update the final state
      updateMessageUI(assistantMessage);
      updateConversationStats();
    }
  }

  // Message Handling
  async function sendMessage() {
    const message = elements.messageInput.value.trim();
    if (!message || !state.apiKey || !state.selectedModel || state.isGenerating)
      return;

    const chat = state.chats[state.currentChatId];
    if (!chat) return;

    // Check if this is the first message in a temporary chat
    const isFirstMessage = chat.temporary && chat.messages.length === 0;

    // Add user message
    const userMessage = {
      role: "user",
      content: message,
      timestamp: Date.now(),
    };
    chat.messages.push(userMessage);

    // Clear input
    elements.messageInput.value = "";
    autoResizeTextarea(elements.messageInput);

    // Mark chat as generating
    chat.isGenerating = true;
    updateChatList();

    // Handle first message in temporary chat
    if (isFirstMessage) {
      // Mark as permanent and save
      chat.temporary = false;

      // Set initial title from first message
      chat.title =
        message.substring(0, 50) + (message.length > 50 ? "..." : "");

      // Save chat and update list
      saveChats();
      updateChatList();

      // Start concurrent title generation
      generateChatTitle(state.currentChatId, message);
    }

    // Update UI
    updateMessagesUI();

    // Prepare assistant message
    const assistantMessage = {
      role: "assistant",
      content: "",
      reasoning: "",
      timestamp: Date.now(),
      streaming: true,
      startTime: performance.now(),
      firstTokenTime: null,
    };
    chat.messages.push(assistantMessage);

    // Append the assistant message element directly
    const messageEl = createMessageElement(
      assistantMessage,
      chat.messages.length - 1
    );
    elements.messages.appendChild(messageEl);

    // Scroll to bottom if auto-scroll is enabled
    if (state.autoScroll) {
      elements.messages.scrollTop = elements.messages.scrollHeight;
    }

    // Call the generate function
    await generate(assistantMessage);
  }

  async function handleStreamingResponse(
    response,
    assistantMessage,
    startTime
  ) {
    // Get the actual message reference from the chat
    const chat = state.chats[state.currentChatId];
    if (!chat) return;

    // Find the actual message in the array
    const messageIndex = chat.messages.findIndex(
      (m) => m.timestamp === assistantMessage.timestamp
    );
    if (messageIndex === -1) return;

    const message = chat.messages[messageIndex]; // Use the actual reference
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";

    while (true) {
      const { done, value } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split("\n");
      buffer = lines.pop() || "";

      for (const line of lines) {
        if (line.startsWith("data: ")) {
          const data = line.slice(6);
          if (data === "[DONE]") {
            message.latency = Math.round(performance.now() - message.startTime);
            message.streaming = false;

            // Calculate reasoning duration if not already set (e.g., reasoning-only response)
            if (!message.reasoningDuration && message.reasoningStartTime) {
              message.reasoningEndTime = performance.now();
              message.reasoningDuration =
                (message.reasoningEndTime - message.reasoningStartTime) / 1000;
            }

            // Calculate tokens per second
            if (
              message.latency > 0 &&
              message.usage &&
              message.usage.total_tokens > 0
            ) {
              message.tokensPerSecond =
                message.usage.total_tokens / (message.latency / 1000);
            }

            // Final update to show completion tokens and reasoning tokens
            updateMessageDirectly(message);
            saveChats();
            return;
          }

          try {
            const parsed = JSON.parse(data);
            const content = parsed.choices?.[0]?.delta?.content;
            const reasoning = parsed.choices?.[0]?.delta?.reasoning;

            if (content || reasoning) {
              // Record time to first token
              if (!message.firstTokenTime && (content || reasoning)) {
                message.firstTokenTime = Math.round(
                  performance.now() - message.startTime
                );
              }

              if (content) {
                // Track reasoning duration when content starts
                if (!message.reasoningEndTime && message.reasoningStartTime) {
                  message.reasoningEndTime = performance.now();
                  message.reasoningDuration =
                    (message.reasoningEndTime - message.reasoningStartTime) /
                    1000; // Convert to seconds
                }
                // Auto-collapse reasoning when actual content starts arriving
                if (
                  message.reasoning &&
                  state.expandedReasoning.has(message.timestamp)
                ) {
                  state.expandedReasoning.delete(message.timestamp);
                }

                // Direct update
                message.content += content;
                updateMessageDirectly(message);
              }

              if (reasoning) {
                // Track when reasoning starts
                if (!message.reasoning && !message.reasoningStartTime) {
                  message.reasoningStartTime = performance.now();
                }
                // Append reasoning directly - the API already includes proper formatting
                if (message.reasoning) {
                  // Simply concatenate - the content already has proper line breaks
                  message.reasoning = message.reasoning + reasoning;
                } else {
                  message.reasoning = reasoning;
                }

                // Auto-expand reasoning when it starts coming in (only if no content yet)
                const hasContent =
                  message.content && message.content.trim().length > 0;
                if (
                  !hasContent &&
                  !state.expandedReasoning.has(message.timestamp)
                ) {
                  state.expandedReasoning.add(message.timestamp);
                }
                updateMessageDirectly(message);
              }
            }

            // Update usage info
            if (parsed.usage) {
              message.usage = parsed.usage;
              updateConversationStats();
              // Update the previous user message to show prompt tokens
              updatePreviousUserMessageTokens(message);
              // Update message UI to show tokens
              updateMessageDirectly(message);
            }

            // Check for cache info
            if (parsed.cache_info) {
              message.cacheHit = parsed.cache_info.hit;
            }
          } catch (e) {
            console.error("Failed to parse SSE data:", e);
          }
        }
      }
    }
  }

  async function handleNonStreamingResponse(
    response,
    assistantMessage,
    startTime
  ) {
    // Get the actual message reference from the chat
    const chat = state.chats[state.currentChatId];
    if (!chat) return;

    // Find the actual message in the array
    const messageIndex = chat.messages.findIndex(
      (m) => m.timestamp === assistantMessage.timestamp
    );
    if (messageIndex === -1) return;

    const message = chat.messages[messageIndex]; // Use the actual reference
    const data = await response.json();
    message.content = data.choices?.[0]?.message?.content || "";
    message.reasoning = data.choices?.[0]?.message?.reasoning || "";
    message.latency = Math.round(performance.now() - message.startTime);
    message.usage = data.usage;
    message.cacheHit = data.cache_info?.hit;

    // Calculate tokens per second
    if (
      message.latency > 0 &&
      message.usage &&
      message.usage.total_tokens > 0
    ) {
      message.tokensPerSecond =
        message.usage.total_tokens / (message.latency / 1000);
    }

    // For non-streaming, estimate reasoning duration based on token rate
    if (
      message.reasoning &&
      message.usage?.completion_tokens_details?.reasoning_tokens &&
      message.tokensPerSecond > 0
    ) {
      const reasoningTokens =
        message.usage.completion_tokens_details.reasoning_tokens;
      
      // Estimate reasoning time based on reasoning tokens and rate
      message.reasoningDuration = reasoningTokens / message.tokensPerSecond;
      message.reasoningDurationEstimated = true; // Mark as estimated
    }

    // Update the previous user message to show prompt tokens
    if (message.usage) {
      updatePreviousUserMessageTokens(message);
    }

    if (data.usage) {
      updateConversationStats();
    }

    updateMessagesUI();
  }

  function stopGeneration() {
    if (state.abortController) {
      state.abortController.abort();
    }
  }

  // UI Updates
  function updateUI() {
    // Send button state
    const canSend =
      elements.messageInput.value.trim() &&
      state.apiKey &&
      state.selectedModel &&
      !state.isGenerating;
    elements.sendBtn.disabled = !canSend;

    // Show/hide send vs stop button
    elements.sendBtn.classList.toggle("hidden", state.isGenerating);
    elements.stopBtn.classList.toggle("hidden", !state.isGenerating);
  }

  function updateChatList() {
    elements.chatList.innerHTML = "";

    // Filter out temporary chats
    const permanentChats = Object.values(state.chats).filter(
      (chat) => !chat.temporary
    );

    const sortedChats = permanentChats.sort(
      (a, b) => b.lastModified - a.lastModified
    );

    const groups = groupChatsByTime(sortedChats);

    // Render each group that has chats
    Object.entries(groups).forEach(([groupKey, group]) => {
      if (group.chats.length === 0) return;

      // Create group container
      const groupContainer = document.createElement("div");
      groupContainer.className = "chat-group";

      // Create group header
      const groupHeader = document.createElement("h3");
      groupHeader.className = "chat-group-header";
      groupHeader.textContent = group.label;
      groupContainer.appendChild(groupHeader);

      // Create container for chat items
      const groupItems = document.createElement("div");
      groupItems.className = "chat-group-items";

      // Add chat items to the group
      group.chats.forEach((chat) => {
        const chatItem = document.createElement("div");
        chatItem.className = "chat-item-wrapper";

        const isActive = chat.id === state.currentChatId;
        const isPinned = state.pinnedChats.includes(chat.id);

        chatItem.innerHTML = `
          <div class="chat-item ${isActive ? "active" : ""}">
            <button class="chat-item-button" data-chat-id="${chat.id}">
              <div class="chat-item-content">
                <span class="chat-item-title" title="${escapeHtml(
                  chat.title
                )}">${escapeHtml(chat.title)}</span>
              </div>
            </button>
            ${
              chat.isGenerating
                ? `
              <div class="chat-item-generating">
                <svg class="icon loading-spinner" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M12 2a10 10 0 1 0 10 10" stroke-linecap="round"/>
                </svg>
              </div>
            `
                : `
              <div class="chat-item-actions">
                <div class="chat-item-gradient"></div>
                <button class="chat-action-btn ${
                  isPinned ? "pinned" : ""
                }" data-action="pin" data-chat-id="${chat.id}" title="${
                    isPinned ? "Unpin chat" : "Pin chat"
                  }">
                  <svg class="icon" viewBox="0 0 24 24" fill="${
                    isPinned ? "currentColor" : "none"
                  }" stroke="currentColor" stroke-width="2">
                    <path d="M12 17v5"></path>
                    <path d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z"></path>
                  </svg>
                </button>
                <button class="chat-action-btn" data-action="rename" data-chat-id="${
                  chat.id
                }" title="Rename chat">
                  <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path>
                    <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"></path>
                  </svg>
                </button>
                <button class="chat-action-btn chat-action-delete" data-action="delete" data-chat-id="${
                  chat.id
                }" title="Delete chat">
                  <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M18 6 6 18"></path>
                    <path d="m6 6 12 12"></path>
                  </svg>
                </button>
              </div>
            `
            }
          </div>
        `;

        // Add click event to the button
        const button = chatItem.querySelector(".chat-item-button");
        button.addEventListener("click", () => loadChat(chat.id));

        // Add action button events only if not generating
        if (!chat.isGenerating) {
          const pinBtn = chatItem.querySelector('[data-action="pin"]');
          const renameBtn = chatItem.querySelector('[data-action="rename"]');
          const deleteBtn = chatItem.querySelector('[data-action="delete"]');

          if (pinBtn) {
            pinBtn.addEventListener("click", (e) => {
              e.stopPropagation();
              togglePinChat(chat.id);
            });
          }

          if (renameBtn) {
            renameBtn.addEventListener("click", (e) => {
              e.stopPropagation();
              renameChat(chat.id);
            });
          }

          if (deleteBtn) {
            deleteBtn.addEventListener("click", (e) => {
              e.stopPropagation();
              if (confirm("Delete this chat?")) {
                deleteChat(chat.id);
              }
            });
          }
        }

        groupItems.appendChild(chatItem);
      });

      groupContainer.appendChild(groupItems);
      elements.chatList.appendChild(groupContainer);
    });
  }

  function filterChatList() {
    const searchTerm = elements.sidebarSearchInput.value.toLowerCase().trim();

    // If no search term, show all chats
    if (!searchTerm) {
      updateChatList();
      return;
    }

    elements.chatList.innerHTML = "";

    // Filter out temporary chats
    const permanentChats = Object.values(state.chats).filter(
      (chat) => !chat.temporary
    );

    const sortedChats = permanentChats.sort(
      (a, b) => b.lastModified - a.lastModified
    );

    const filteredChats = sortedChats.filter((chat) => {
      // Search in chat title
      if (chat.title.toLowerCase().includes(searchTerm)) {
        return true;
      }

      // Search in message content
      return chat.messages.some((message) =>
        message.content.toLowerCase().includes(searchTerm)
      );
    });

    if (filteredChats.length === 0) {
      elements.chatList.innerHTML = `
        <div class="chat-search-empty">
          <p>No chats found for "${escapeHtml(searchTerm)}"</p>
        </div>
      `;
      return;
    }

    // Group the filtered chats
    const groups = groupChatsByTime(filteredChats);

    // Render each group that has chats
    Object.entries(groups).forEach(([groupKey, group]) => {
      if (group.chats.length === 0) return;

      // Create group container
      const groupContainer = document.createElement("div");
      groupContainer.className = "chat-group";

      // Create group header
      const groupHeader = document.createElement("h3");
      groupHeader.className = "chat-group-header";
      groupHeader.textContent = group.label;
      groupContainer.appendChild(groupHeader);

      // Create container for chat items
      const groupItems = document.createElement("div");
      groupItems.className = "chat-group-items";

      // Add chat items to the group
      group.chats.forEach((chat) => {
        const chatItem = document.createElement("div");
        chatItem.className = "chat-item-wrapper";

        const isActive = chat.id === state.currentChatId;
        const isPinned = state.pinnedChats.includes(chat.id);

        chatItem.innerHTML = `
          <div class="chat-item ${isActive ? "active" : ""}">
            <button class="chat-item-button" data-chat-id="${chat.id}">
              <div class="chat-item-content">
                <span class="chat-item-title" title="${escapeHtml(
                  chat.title
                )}">${escapeHtml(chat.title)}</span>
              </div>
            </button>
            ${
              chat.isGenerating
                ? `
              <div class="chat-item-generating">
                <svg class="icon loading-spinner" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M12 2a10 10 0 1 0 10 10" stroke-linecap="round"/>
                </svg>
              </div>
            `
                : `
              <div class="chat-item-actions">
                <div class="chat-item-gradient"></div>
                <button class="chat-action-btn ${
                  isPinned ? "pinned" : ""
                }" data-action="pin" data-chat-id="${chat.id}" title="${
                    isPinned ? "Unpin chat" : "Pin chat"
                  }">
                  <svg class="icon" viewBox="0 0 24 24" fill="${
                    isPinned ? "currentColor" : "none"
                  }" stroke="currentColor" stroke-width="2">
                    <path d="M12 17v5"></path>
                    <path d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z"></path>
                  </svg>
                </button>
                <button class="chat-action-btn" data-action="rename" data-chat-id="${
                  chat.id
                }" title="Rename chat">
                  <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path>
                    <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"></path>
                  </svg>
                </button>
                <button class="chat-action-btn chat-action-delete" data-action="delete" data-chat-id="${
                  chat.id
                }" title="Delete chat">
                  <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M18 6 6 18"></path>
                    <path d="m6 6 12 12"></path>
                  </svg>
                </button>
              </div>
            `
            }
          </div>
        `;

        // Add click event to the button
        const button = chatItem.querySelector(".chat-item-button");
        button.addEventListener("click", () => loadChat(chat.id));

        // Add action button events only if not generating
        if (!chat.isGenerating) {
          const pinBtn = chatItem.querySelector('[data-action="pin"]');
          const renameBtn = chatItem.querySelector('[data-action="rename"]');
          const deleteBtn = chatItem.querySelector('[data-action="delete"]');

          if (pinBtn) {
            pinBtn.addEventListener("click", (e) => {
              e.stopPropagation();
              togglePinChat(chat.id);
            });
          }

          if (renameBtn) {
            renameBtn.addEventListener("click", (e) => {
              e.stopPropagation();
              renameChat(chat.id);
            });
          }

          if (deleteBtn) {
            deleteBtn.addEventListener("click", (e) => {
              e.stopPropagation();
              if (confirm("Delete this chat?")) {
                deleteChat(chat.id);
              }
            });
          }
        }

        groupItems.appendChild(chatItem);
      });

      groupContainer.appendChild(groupItems);
      elements.chatList.appendChild(groupContainer);
    });
  }

  function updateMessagesUI() {
    const chat = state.chats[state.currentChatId];
    if (!chat) return;

    elements.messages.innerHTML = "";

    if (chat.messages.length === 0) {
      elements.messages.innerHTML = `
                <div class="welcome-message">
                    <h2>Welcome to ${escapeHtml(
                      config.title || "Starport LLM Chat"
                    )}</h2>
                    <p>Select a model and start chatting!</p>
                </div>
            `;
      return;
    }

    chat.messages.forEach((message, index) => {
      const messageEl = createMessageElement(message, index);
      elements.messages.appendChild(messageEl);
    });

    // Render math in all messages
    if (typeof renderMathInElement !== "undefined") {
      const messageElements = elements.messages.querySelectorAll(".message");
      messageElements.forEach((msgEl) => {
        renderMathInMessage(msgEl);
      });
    }

    // Render code and diagrams
    renderCodeAndDiagrams(elements.messages);

    // Scroll to bottom if auto-scroll is enabled
    if (state.autoScroll) {
      elements.messages.scrollTop = elements.messages.scrollHeight;
    }
  }

  function updateMessageDirectly(message) {
    const messageEl = document.getElementById(`message-${message.timestamp}`);
    if (!messageEl) return;

    // Update streaming class
    messageEl.className = `message ${message.role}${
      message.streaming ? " streaming" : ""
    }`;

    // Update content
    const textEl = messageEl.querySelector(".message-text");
    if (textEl) {
      // Simply update content without any fade effects
      if (message.role === "user") {
        textEl.innerHTML = escapeHtml(message.content);
      } else if (message.streaming && !message.content && !message.reasoning) {
        textEl.innerHTML = '<div class="thinking-indicator">Thinking<span class="thinking-dots"></span></div>';
      } else {
        textEl.innerHTML = formatMessageContent(message.content);
        renderSpecialContent(textEl);
      }
    }

    // Update or add reasoning section
    if (message.reasoning) {
      let reasoningEl = messageEl.querySelector(".message-reasoning");
      if (!reasoningEl) {
        // Create reasoning section if it doesn't exist
        const contentEl = messageEl.querySelector(".message-content");
        const textEl = messageEl.querySelector(".message-text");
        if (contentEl && textEl) {
          reasoningEl = document.createElement("div");
          reasoningEl.className = "message-reasoning";
          const hasContent =
            message.content && message.content.trim().length > 0;
          const reasoningStreaming =
            message.streaming && message.reasoning && !hasContent;
          reasoningEl.innerHTML = `
                        <button class="reasoning-toggle${
                          reasoningStreaming ? " reasoning-streaming" : ""
                        }" onclick="toggleReasoning('${message.timestamp}')">
                            ${
                              reasoningStreaming
                                ? `
                                <svg class="icon loading-spinner" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                                    <path d="M12 2a10 10 0 1 0 10 10" stroke-linecap="round"/>
                                </svg>
                                Reasoning...
                            `
                                : `
                                <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                                    <path d="M12 18V5"/>
                                    <path d="M15 13a4.17 4.17 0 0 1-3-4 4.17 4.17 0 0 1-3 4"/>
                                    <path d="M17.598 6.5A3 3 0 1 0 12 5a3 3 0 1 0-5.598 1.5"/>
                                    <path d="M17.997 5.125a4 4 0 0 1 2.526 5.77"/>
                                    <path d="M18 18a4 4 0 0 0 2-7.464"/>
                                    <path d="M19.967 17.483A4 4 0 1 1 12 18a4 4 0 1 1-7.967-.517"/>
                                    <path d="M6 18a4 4 0 0 1-2-7.464"/>
                                    <path d="M6.003 5.125a4 4 0 0 0-2.526 5.77"/>
                                    <!-- Circuit-like nodes -->
                                    <circle cx="12" cy="5" r="1" fill="currentColor"/>
                                    <circle cx="12" cy="9" r="0.5" fill="currentColor"/>
                                    <circle cx="9" cy="13" r="0.5" fill="currentColor"/>
                                    <circle cx="15" cy="13" r="0.5" fill="currentColor"/>
                                    <circle cx="12" cy="18" r="1" fill="currentColor"/>
                                    <!-- Connection dots -->
                                    <circle cx="6" cy="8" r="0.3" fill="currentColor"/>
                                    <circle cx="18" cy="8" r="0.3" fill="currentColor"/>
                                    <circle cx="6" cy="15" r="0.3" fill="currentColor"/>
                                    <circle cx="18" cy="15" r="0.3" fill="currentColor"/>
                                </svg>
                                ${
                                  message.reasoningDuration
                                    ? `Thought for <span style="color: var(--text-tertiary);">${
                                        message.reasoningDurationEstimated
                                          ? "~"
                                          : ""
                                      }${message.reasoningDuration.toFixed(
                                        3
                                      )}s</span>`
                                    : "Reasoning"
                                }
                            `
                            }
                        </button>
                        <div class="reasoning-content ${
                          state.expandedReasoning.has(message.timestamp)
                            ? "expanded"
                            : "collapsed"
                        }">
                            <div class="reasoning-inner">
                                ${formatMessageContent(message.reasoning, true)}
                            </div>
                        </div>
                    `;
          // Insert before message-text (reasoning should appear above the response)
          textEl.insertAdjacentElement("beforebegin", reasoningEl);
        }
      } else {
        // Update existing reasoning content
        const innerEl = reasoningEl.querySelector(".reasoning-inner");
        if (innerEl) {
          innerEl.innerHTML = formatMessageContent(message.reasoning, true);
          // Auto-scroll to bottom during streaming
          if (
            message.streaming &&
            state.expandedReasoning.has(message.timestamp)
          ) {
            requestAnimationFrame(() => {
              innerEl.scrollTop = innerEl.scrollHeight;
            });
          }
        }

        // Update toggle button and visibility state
        const toggleBtn = reasoningEl.querySelector(".reasoning-toggle");
        const reasoningContent =
          reasoningEl.querySelector(".reasoning-content");

        if (toggleBtn) {
          const hasContent =
            message.content && message.content.trim().length > 0;
          const reasoningStreaming =
            message.streaming && message.reasoning && !hasContent;
          toggleBtn.className = `reasoning-toggle${
            reasoningStreaming ? " reasoning-streaming" : ""
          }`;
          toggleBtn.innerHTML = reasoningStreaming
            ? `
                        <svg class="icon loading-spinner" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                            <path d="M12 2a10 10 0 1 0 10 10" stroke-linecap="round"/>
                        </svg>
                        Reasoning...
                    `
            : `
                        <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                            <path d="M12 18V5"/>
                            <path d="M15 13a4.17 4.17 0 0 1-3-4 4.17 4.17 0 0 1-3 4"/>
                            <path d="M17.598 6.5A3 3 0 1 0 12 5a3 3 0 1 0-5.598 1.5"/>
                            <path d="M17.997 5.125a4 4 0 0 1 2.526 5.77"/>
                            <path d="M18 18a4 4 0 0 0 2-7.464"/>
                            <path d="M19.967 17.483A4 4 0 1 1 12 18a4 4 0 1 1-7.967-.517"/>
                            <path d="M6 18a4 4 0 0 1-2-7.464"/>
                            <path d="M6.003 5.125a4 4 0 0 0-2.526 5.77"/>
                            <!-- Circuit-like nodes -->
                            <circle cx="12" cy="5" r="1" fill="currentColor"/>
                            <circle cx="12" cy="9" r="0.5" fill="currentColor"/>
                            <circle cx="9" cy="13" r="0.5" fill="currentColor"/>
                            <circle cx="15" cy="13" r="0.5" fill="currentColor"/>
                            <circle cx="12" cy="18" r="1" fill="currentColor"/>
                            <!-- Connection dots -->
                            <circle cx="6" cy="8" r="0.3" fill="currentColor"/>
                            <circle cx="18" cy="8" r="0.3" fill="currentColor"/>
                            <circle cx="6" cy="15" r="0.3" fill="currentColor"/>
                            <circle cx="18" cy="15" r="0.3" fill="currentColor"/>
                        </svg>
                        ${
                          message.reasoningDuration
                            ? `Thought for <span style="color: var(--text-tertiary);">${
                                message.reasoningDurationEstimated
                                  ? "~"
                                  : ""
                              }${message.reasoningDuration.toFixed(
                                3
                              )}s</span>`
                            : "Reasoning"
                        }
                    `;
        }

        if (reasoningContent) {
          // Update the expanded/collapsed class
          reasoningContent.className = `reasoning-content ${
            state.expandedReasoning.has(message.timestamp)
              ? "expanded"
              : "collapsed"
          }`;
        }
      }
    }

    // Update metadata
    const metadataEl = messageEl.querySelector(".message-metadata");
    if (metadataEl) {
      updateMessageMetadata(metadataEl, message);
    }

    // Update message actions (show only when not streaming)
    if (message.role === "assistant") {
      let actionsEl = messageEl.querySelector(".message-actions");
      const contentEl = messageEl.querySelector(".message-content");

      if (!message.streaming && !actionsEl && contentEl) {
        // Create actions when streaming ends
        actionsEl = document.createElement("div");
        actionsEl.className = "message-actions";
        actionsEl.innerHTML = `
          <button class="btn btn-ghost" onclick="copyMessage('${message.timestamp}')">Copy</button>
          <button class="btn btn-ghost" onclick="regenerateMessage('${message.timestamp}')">Regenerate</button>
        `;
        contentEl.appendChild(actionsEl);
      } else if (message.streaming && actionsEl) {
        // Remove actions during streaming
        actionsEl.remove();
      }
    }

    // Save periodically during streaming
    if (message.streaming && Math.random() < 0.1) {
      // Save ~10% of updates
      saveChats();
    }

    // Auto-scroll during streaming if enabled
    if (message.streaming && state.autoScroll && !state.userScrolled) {
      requestAnimationFrame(() => {
        elements.messages.scrollTop = elements.messages.scrollHeight;
      });
    }
  }

  function updateMessageMetadata(metadataEl, message) {
    let metadataHtml = "";

    if (message.role === "assistant" && message.usage) {
      // Determine which phase is streaming
      const hasContent = message.content && message.content.trim().length > 0;
      const reasoningStreaming = !!(
        message.streaming &&
        message.reasoning &&
        !hasContent
      );
      const contentStreaming = !!(message.streaming && hasContent);

      // Show reasoning tokens first if available
      if (message.usage.completion_tokens_details?.reasoning_tokens) {
        const reasoningTokens =
          message.usage.completion_tokens_details.reasoning_tokens;
        if (reasoningStreaming) {
          metadataHtml += `<span class="token-badge streaming" title="Reasoning tokens">Reasoning: ${reasoningTokens.toLocaleString()} tok</span>`;
        } else {
          metadataHtml += `<span class="token-badge" title="Reasoning tokens">Reasoning: ${reasoningTokens.toLocaleString()} tok</span>`;
        }
      }

      // Then show completion tokens
      const completionTokens = message.usage.completion_tokens || 0;
      const tokenBadgeClass = contentStreaming
        ? "token-badge streaming"
        : "token-badge";
      metadataHtml += `<span class="${tokenBadgeClass}" title="Completion tokens generated">Completion: ${completionTokens.toLocaleString()} tok</span>`;
    }

    if (message.firstTokenTime) {
      metadataHtml += `<span class="latency-badge" title="Time to First Token (milliseconds)">TTFT: ${formatLatency(
        message.firstTokenTime
      )}</span>`;
    }
    if (message.latency) {
      metadataHtml += `<span class="latency-badge" title="Total response latency (milliseconds)">Latency: ${formatLatency(
        message.latency
      )}</span>`;
    }
    if (message.tokensPerSecond) {
      metadataHtml += `<span class="latency-badge" title="Tokens per second (includes reasoning)">TPS: ${message.tokensPerSecond.toFixed(
        1
      )} tok/s</span>`;
    }
    if (message.cacheHit !== undefined) {
      metadataHtml += `<span class="cache-badge ${
        message.cacheHit ? "hit" : "miss"
      }" title="Whether this response was served from cache">${
        message.cacheHit ? "Cache Hit" : "Cache Miss"
      }</span>`;
    }

    metadataEl.innerHTML = metadataHtml;
  }

  function updatePreviousUserMessageTokens(assistantMessage) {
    const chat = state.chats[state.currentChatId];
    if (!chat || !assistantMessage.usage) return;

    // Find the index of this assistant message
    const assistantIndex = chat.messages.findIndex(
      (m) => m.timestamp === assistantMessage.timestamp
    );
    if (assistantIndex <= 0) return;

    // Get the previous message (should be user message)
    const userMessage = chat.messages[assistantIndex - 1];
    if (userMessage.role !== "user") return;

    // Find the user message element and update its metadata
    const messageElements = elements.messages.querySelectorAll(".message");
    const userMessageEl = messageElements[assistantIndex - 1];
    if (userMessageEl) {
      const metadataEl = userMessageEl.querySelector(".message-metadata");
      if (metadataEl) {
        metadataEl.innerHTML = `<span class="token-badge" title="Total prompt tokens">Prompt: ${assistantMessage.usage.prompt_tokens.toLocaleString()} tok</span>`;
      }
    }
  }

  function updateMessageUI(message) {
    // Get the current chat to find message index
    const chat = state.chats[state.currentChatId];
    if (!chat) return;

    // Find the message index
    const messageIndex = chat.messages.findIndex(
      (m) => m.timestamp === message.timestamp
    );
    if (messageIndex === -1) {
      console.error("Message not found in chat messages");
      return;
    }

    // Find the message element by index (more reliable than timestamp)
    const messageElements = elements.messages.querySelectorAll(".message");
    const messageEl = messageElements[messageIndex];

    if (messageEl) {
      // Update message element classes (for streaming state)
      messageEl.className = `message ${message.role}${
        message.streaming ? " streaming" : ""
      }`;

      // Update text content
      const textEl = messageEl.querySelector(".message-text");
      if (textEl) {
        if (message.role === "user") {
          textEl.innerHTML = escapeHtml(message.content);
        } else {
          let fullHTML = formatMessageContent(message.content);
          textEl.innerHTML = fullHTML;

          // Render special content after final update
          if (!message.streaming) {
            renderSpecialContent(textEl);
          }
        }
      }

      // Always update metadata
      const metadataEl = messageEl.querySelector(".message-metadata");
      if (metadataEl) {
        updateMessageMetadata(metadataEl, message);
      }

      // Update or add reasoning section
      if (message.reasoning) {
        let reasoningEl = messageEl.querySelector(".message-reasoning");
        if (!reasoningEl) {
          // Create reasoning section if it doesn't exist
          const contentEl = messageEl.querySelector(".message-content");
          const textEl = messageEl.querySelector(".message-text");
          if (contentEl && textEl) {
            reasoningEl = document.createElement("div");
            reasoningEl.className = "message-reasoning";
            const hasContent =
              message.content && message.content.trim().length > 0;
            const reasoningStreaming =
              message.streaming && message.reasoning && !hasContent;
            reasoningEl.innerHTML = `
                            <button class="reasoning-toggle${
                              reasoningStreaming ? " reasoning-streaming" : ""
                            }" onclick="toggleReasoning('${
              message.timestamp
            }')">
                                ${
                                  reasoningStreaming
                                    ? `
                                    <svg class="icon loading-spinner" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                                        <path d="M12 2a10 10 0 1 0 10 10" stroke-linecap="round"/>
                                    </svg>
                                    Reasoning...
                                `
                                    : `
                                    <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                                        <path d="M12 18V5"/>
                                        <path d="M15 13a4.17 4.17 0 0 1-3-4 4.17 4.17 0 0 1-3 4"/>
                                        <path d="M17.598 6.5A3 3 0 1 0 12 5a3 3 0 1 0-5.598 1.5"/>
                                        <path d="M17.997 5.125a4 4 0 0 1 2.526 5.77"/>
                                        <path d="M18 18a4 4 0 0 0 2-7.464"/>
                                        <path d="M19.967 17.483A4 4 0 1 1 12 18a4 4 0 1 1-7.967-.517"/>
                                        <path d="M6 18a4 4 0 0 1-2-7.464"/>
                                        <path d="M6.003 5.125a4 4 0 0 0-2.526 5.77"/>
                                        <!-- Circuit-like nodes -->
                                        <circle cx="12" cy="5" r="1" fill="currentColor"/>
                                        <circle cx="12" cy="9" r="0.5" fill="currentColor"/>
                                        <circle cx="9" cy="13" r="0.5" fill="currentColor"/>
                                        <circle cx="15" cy="13" r="0.5" fill="currentColor"/>
                                        <circle cx="12" cy="18" r="1" fill="currentColor"/>
                                        <!-- Connection dots -->
                                        <circle cx="6" cy="8" r="0.3" fill="currentColor"/>
                                        <circle cx="18" cy="8" r="0.3" fill="currentColor"/>
                                        <circle cx="6" cy="15" r="0.3" fill="currentColor"/>
                                        <circle cx="18" cy="15" r="0.3" fill="currentColor"/>
                                    </svg>
                                    Reasoning
                                `
                                }
                            </button>
                            <div class="reasoning-content ${
                              state.expandedReasoning.has(message.timestamp)
                                ? "expanded"
                                : "collapsed"
                            }">
                                <div class="reasoning-inner">
                                    ${formatMessageContent(
                                      message.reasoning,
                                      true
                                    )}
                                </div>
                            </div>
                        `;
            // Insert before message-text (reasoning should appear above the response)
            textEl.insertAdjacentElement("beforebegin", reasoningEl);
          }
        } else {
          // Update existing reasoning content
          const reasoningContentEl =
            reasoningEl.querySelector(".reasoning-content");
          if (reasoningContentEl) {
            // Find or create the inner div
            let innerEl = reasoningContentEl.querySelector(".reasoning-inner");
            if (!innerEl) {
              innerEl = document.createElement("div");
              innerEl.className = "reasoning-inner";
              reasoningContentEl.appendChild(innerEl);
            }
            innerEl.innerHTML = formatMessageContent(message.reasoning, true);
            // Auto-scroll to bottom of reasoning content during streaming
            if (message.streaming) {
              // Small delay to ensure DOM updates
              requestAnimationFrame(() => {
                innerEl.scrollTop = innerEl.scrollHeight;
              });
            }
            // Update classes (no streaming class for reasoning)
            reasoningContentEl.className = `reasoning-content ${
              state.expandedReasoning.has(message.timestamp)
                ? "expanded"
                : "collapsed"
            }`;
          }
          // Update toggle button
          const toggleBtn = reasoningEl.querySelector(".reasoning-toggle");
          if (toggleBtn) {
            // Don't update innerHTML here - it's already set correctly above
            // Add/remove reasoning-streaming class based on whether reasoning is still streaming
            const hasContent =
              message.content && message.content.trim().length > 0;
            const reasoningStreaming =
              message.streaming && message.reasoning && !hasContent;
            if (reasoningStreaming) {
              toggleBtn.classList.add("reasoning-streaming");
            } else {
              toggleBtn.classList.remove("reasoning-streaming");
            }
          }
        }
      }

      // Render math in the message
      renderMathInMessage(messageEl);

      // Render code highlighting and diagrams
      renderCodeAndDiagrams(messageEl);

      // Scroll to bottom to show new content
      elements.messages.scrollTop = elements.messages.scrollHeight;
    }
  }

  function createMessageElement(message, index) {
    const div = document.createElement("div");
    div.className = `message ${message.role}${
      message.streaming ? " streaming" : ""
    }`;
    div.id = `message-${message.timestamp}`;
    div.setAttribute("data-timestamp", String(message.timestamp));
    div.setAttribute("data-message-index", String(index));

    const avatar = message.role === "user" ? "U" : "A";
    const roleDisplay = message.role === "user" ? "You" : "Assistant";

    let metadataHtml = "";

    // For user messages, show total prompt tokens from the next assistant message
    if (message.role === "user") {
      const chat = state.chats[state.currentChatId];
      if (chat && index < chat.messages.length - 1) {
        const nextMessage = chat.messages[index + 1];
        if (
          nextMessage.role === "assistant" &&
          nextMessage.usage &&
          nextMessage.usage.prompt_tokens
        ) {
          metadataHtml += `<span class="token-badge" title="Total prompt tokens">Prompt: ${nextMessage.usage.prompt_tokens.toLocaleString()} tok</span>`;
        }
      }
    } else if (message.role === "assistant" && message.usage) {
      // Determine which phase is streaming
      const hasContent = message.content && message.content.trim().length > 0;
      const reasoningStreaming = !!(
        message.streaming &&
        message.reasoning &&
        !hasContent
      );
      const contentStreaming = !!(message.streaming && hasContent);

      // Show reasoning tokens first if available
      if (message.usage.completion_tokens_details?.reasoning_tokens) {
        const reasoningTokens =
          message.usage.completion_tokens_details.reasoning_tokens;
        // Only apply inline styles when NOT streaming (to show blue when static)
        if (reasoningStreaming) {
          metadataHtml += `<span class="token-badge streaming" title="Reasoning tokens">Reasoning: ${reasoningTokens.toLocaleString()} tok</span>`;
        } else {
          metadataHtml += `<span class="token-badge" title="Reasoning tokens">Reasoning: ${reasoningTokens.toLocaleString()} tok</span>`;
        }
      }

      // Then show completion tokens
      const completionTokens = message.usage.completion_tokens || 0;
      metadataHtml += `<span class="token-badge${
        contentStreaming ? " streaming" : ""
      }" title="Completion tokens generated">Completion: ${completionTokens.toLocaleString()} tok</span>`;
    }

    if (message.firstTokenTime) {
      metadataHtml += `<span class="latency-badge" title="Time to First Token (milliseconds)">TTFT: ${formatLatency(
        message.firstTokenTime
      )}</span>`;
    }
    if (message.latency) {
      metadataHtml += `<span class="latency-badge" title="Total response latency (milliseconds)">Latency: ${formatLatency(
        message.latency
      )}</span>`;
    }
    if (message.tokensPerSecond) {
      metadataHtml += `<span class="latency-badge" title="Tokens per second (includes reasoning)">TPS: ${message.tokensPerSecond.toFixed(
        1
      )} tok/s</span>`;
    }
    if (message.cacheHit !== undefined) {
      metadataHtml += `<span class="cache-badge ${
        message.cacheHit ? "hit" : "miss"
      }" title="Whether this response was served from cache">${
        message.cacheHit ? "Cache Hit" : "Cache Miss"
      }</span>`;
    }

    div.innerHTML = `
            <div class="message-avatar">${avatar}</div>
            <div class="message-content">
                <div class="message-header">
                    <span class="message-role">${roleDisplay}</span>
                    <div class="message-metadata">${metadataHtml}</div>
                </div>
                ${
                  message.reasoning
                    ? `
                <div class="message-reasoning">
                    <button class="reasoning-toggle${
                      message.streaming &&
                      message.reasoning &&
                      (!message.content || message.content.trim().length === 0)
                        ? " reasoning-streaming"
                        : ""
                    }" onclick="toggleReasoning('${message.timestamp}')">
                        ${
                          message.streaming &&
                          message.reasoning &&
                          (!message.content ||
                            message.content.trim().length === 0)
                            ? `
                            <svg class="icon loading-spinner" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                                <path d="M12 2a10 10 0 1 0 10 10" stroke-linecap="round"/>
                            </svg>
                            Reasoning...
                        `
                            : `
                            <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                                <path d="M12 18V5"/>
                                <path d="M15 13a4.17 4.17 0 0 1-3-4 4.17 4.17 0 0 1-3 4"/>
                                <path d="M17.598 6.5A3 3 0 1 0 12 5a3 3 0 1 0-5.598 1.5"/>
                                <path d="M17.997 5.125a4 4 0 0 1 2.526 5.77"/>
                                <path d="M18 18a4 4 0 0 0 2-7.464"/>
                                <path d="M19.967 17.483A4 4 0 1 1 12 18a4 4 0 1 1-7.967-.517"/>
                                <path d="M6 18a4 4 0 0 1-2-7.464"/>
                                <path d="M6.003 5.125a4 4 0 0 0-2.526 5.77"/>
                                <!-- Circuit-like nodes -->
                                <circle cx="12" cy="5" r="1" fill="currentColor"/>
                                <circle cx="12" cy="9" r="0.5" fill="currentColor"/>
                                <circle cx="9" cy="13" r="0.5" fill="currentColor"/>
                                <circle cx="15" cy="13" r="0.5" fill="currentColor"/>
                                <circle cx="12" cy="18" r="1" fill="currentColor"/>
                                <!-- Connection dots -->
                                <circle cx="6" cy="8" r="0.3" fill="currentColor"/>
                                <circle cx="18" cy="8" r="0.3" fill="currentColor"/>
                                <circle cx="6" cy="15" r="0.3" fill="currentColor"/>
                                <circle cx="18" cy="15" r="0.3" fill="currentColor"/>
                            </svg>
                            ${
                              message.reasoningDuration
                                ? `Thought for <span style="color: var(--text-tertiary);">${
                                    message.reasoningDurationEstimated
                                      ? "~"
                                      : ""
                                  }${message.reasoningDuration.toFixed(
                                    3
                                  )}s</span>`
                                : "Reasoning"
                            }
                        `
                        }
                    </button>
                    <div class="reasoning-content ${
                      state.expandedReasoning.has(message.timestamp)
                        ? "expanded"
                        : "collapsed"
                    }">
                        <div class="reasoning-inner">
                            ${formatMessageContent(message.reasoning, true)}
                        </div>
                    </div>
                </div>
                `
                    : ""
                }
                <div class="message-text">
                    ${message.role === "assistant" && message.streaming && !message.content && !message.reasoning 
                        ? '<div class="thinking-indicator">Thinking<span class="thinking-dots"></span></div>'
                        : message.role === "user" 
                            ? escapeHtml(message.content) 
                            : formatMessageContent(message.content)}
                </div>
                ${
                  message.role === "assistant" && !message.streaming
                    ? `
                <div class="message-actions">
                    <button class="btn btn-ghost" onclick="copyMessage('${message.timestamp}')">Copy</button>
                    <button class="btn btn-ghost" onclick="regenerateMessage('${message.timestamp}')">Regenerate</button>
                </div>
                `
                    : ""
                }
            </div>
        `;

    return div;
  }

  // Model Management
  async function loadModels() {
    try {
      // If no API key is set, show a helpful message
      if (!state.apiKey) {
        updateModelSelect([]);
        elements.modelSelect.innerHTML =
          '<option value="">Set API key in settings first</option>';
        return;
      }

      const response = await fetch(`${config.apiBaseURL}/api/v1/models`, {
        headers: { Authorization: `Bearer ${state.apiKey}` },
      });

      if (!response.ok) {
        if (response.status === 401) {
          updateModelSelect([]);
          elements.modelSelect.innerHTML =
            '<option value="">Invalid API key - check settings</option>';
          return;
        }
        throw new Error(`Failed to load models: ${response.status}`);
      }

      const data = await response.json();
      state.models = data.data || [];

      // Build pricing lookup
      state.modelPricing = {};
      state.models.forEach((model) => {
        if (model.pricing) {
          state.modelPricing[model.id] = model.pricing;
        }
      });

      updateModelSelect(state.models);
      updateModelPricing();
      updateConversationStats();
    } catch (error) {
      console.error("Failed to load models:", error);
      updateModelSelect([]);
      elements.modelSelect.innerHTML =
        '<option value="">Error loading models</option>';
    }
  }

  function updateModelSelect(models) {
    elements.modelSelect.innerHTML = "";

    if (models.length === 0) {
      elements.modelSelect.innerHTML =
        '<option value="">No models available</option>';
      return;
    }

    // Group models by provider
    const modelsByProvider = {};
    models.forEach((model) => {
      const [provider] = model.id.split("/");
      if (!modelsByProvider[provider]) {
        modelsByProvider[provider] = [];
      }
      modelsByProvider[provider].push(model);
    });

    // Create optgroups
    Object.entries(modelsByProvider).forEach(([provider, providerModels]) => {
      const optgroup = document.createElement("optgroup");
      optgroup.label = provider.charAt(0).toUpperCase() + provider.slice(1);

      providerModels.forEach((model) => {
        const option = document.createElement("option");
        option.value = model.id;
        option.textContent = model.id.split("/")[1];

        if (model.id === state.selectedModel) {
          option.selected = true;
        }

        optgroup.appendChild(option);
      });

      elements.modelSelect.appendChild(optgroup);
    });

    // If no model is selected, select the first one
    if (!state.selectedModel && models.length > 0) {
      state.selectedModel = models[0].id;
      elements.modelSelect.value = state.selectedModel;
      localStorage.setItem("starport_model", state.selectedModel);
    }
  }

  // API Key Generation
  async function generateAPIKey() {
    try {
      const response = await fetch(`${config.apiBaseURL}/chat/generate-key`, {
        method: "POST",
      });

      if (!response.ok) {
        const error = await response.json();
        throw new Error(error.error || `HTTP ${response.status}`);
      }

      const data = await response.json();
      elements.apiKeyInput.value = data.key;
      state.apiKey = data.key;
      localStorage.setItem("starport_api_key", state.apiKey);
      updateUI();

      // Load models with the new key
      await loadModels();

      showToast("API key generated and saved successfully!", "success");

      // Close the modal after a short delay
      setTimeout(() => {
        hideModal(elements.settingsModal);
      }, 1500);
    } catch (error) {
      showError("Failed to generate API key", error);
    }
  }

  function updateModelPricing() {
    const pricing =
      state.modelPricing && state.selectedModel
        ? state.modelPricing[state.selectedModel]
        : null;

    if (pricing) {
      // Convert from per-1k to per-1M tokens
      const promptPricePerMillion = parseFloat(pricing.prompt) * 1000;
      const completionPricePerMillion = parseFloat(pricing.completion) * 1000;

      // Format prices based on magnitude
      const formatPrice = (price) => {
        if (price >= 1) {
          return price.toFixed(2);
        } else if (price >= 0.01) {
          return price.toFixed(3);
        } else if (price > 0) {
          return price.toFixed(4);
        } else {
          return "0.00";
        }
      };

      elements.modelPricing.innerHTML = `
                <span>$${formatPrice(promptPricePerMillion)}1/M tok ↓</span>
                <span style="margin-left: 8px;">$${formatPrice(
                  completionPricePerMillion
                )}1/M tok ↑</span>
            `;
    } else {
      elements.modelPricing.textContent = "";
    }
  }

  function updateConversationStats() {
    const chat = state.chats[state.currentChatId];
    if (!chat) {
      // Clear stats display when no chat
      elements.tokenCount.textContent = "↓ 0 ↑ 0";
      elements.costEstimate.textContent = "";
      return;
    }

    let totalPromptTokens = 0;
    let totalCompletionTokens = 0;
    let totalCost = 0;

    // Get current model pricing (may not be loaded yet)
    const pricing =
      state.modelPricing && state.selectedModel
        ? state.modelPricing[state.selectedModel]
        : null;

    // Calculate totals from all messages
    chat.messages.forEach((message) => {
      if (message.usage) {
        totalPromptTokens += message.usage.prompt_tokens || 0;
        totalCompletionTokens += message.usage.completion_tokens || 0;

        // Calculate cost for this message if we have pricing
        if (pricing) {
          // Pricing is stored per 1k tokens, so divide by 1000
          const promptCost =
            (message.usage.prompt_tokens / 1000) * parseFloat(pricing.prompt);
          const completionCost =
            (message.usage.completion_tokens / 1000) *
            parseFloat(pricing.completion);
          totalCost += promptCost + completionCost;
        }
      }
    });

    // Update token count display
    const hasStreamingMessages = chat.messages.some(
      (m) => m.role === "assistant" && !m.usage
    );
    elements.tokenCount.innerHTML = `
            <div style="display: flex; gap: 12px; align-items: center; font-size: 13px;">
                <span title="Prompt tokens">↓ ${totalPromptTokens.toLocaleString()} tok</span>
                <span title="Completion tokens">↑ ${totalCompletionTokens.toLocaleString()} tok</span>
                ${
                  hasStreamingMessages && state.streamEnabled
                    ? '<span style="font-size: 10px; color: var(--text-tertiary);" title="Token counts not available for streaming responses">*</span>'
                    : ""
                }
            </div>
        `;

    // Update cost display
    if (totalCost > 0) {
      // Format cost with appropriate precision
      let costDisplay;
      if (totalCost < 0.01) {
        costDisplay = `$${totalCost.toFixed(6)}`;
      } else if (totalCost < 1) {
        costDisplay = `$${totalCost.toFixed(4)}`;
      } else {
        costDisplay = `$${totalCost.toFixed(2)}`;
      }

      elements.costEstimate.innerHTML = `<span style="font-size: 13px; font-weight: 500;">Cost: ${costDisplay}</span>`;
    } else {
      elements.costEstimate.textContent = "";
    }
  }

  // Error Handling
  function showError(message, error) {
    elements.toastMessage.textContent = message;

    if (error) {
      elements.errorJson.textContent = JSON.stringify(error, null, 2);
    }

    elements.errorToast.classList.remove("hidden");

    // Auto-hide after 10 seconds
    setTimeout(hideError, 10000);
  }

  function hideError() {
    elements.errorToast.classList.add("hidden");
    elements.toastDetails.classList.add("hidden");
  }

  // Toast notifications
  function showToast(message, type = "info") {
    // For now, use the error toast for all notifications
    elements.toastMessage.textContent = message;
    elements.errorToast.className = `toast toast-${type}`;
    elements.errorToast.classList.remove("hidden");

    // Auto-hide after 3 seconds for success messages
    setTimeout(() => {
      elements.errorToast.classList.add("hidden");
    }, 3000);
  }

  // Keyboard Shortcuts
  function handleKeyboardShortcuts(e) {
    // Cmd/Ctrl + K: Focus search
    if ((e.metaKey || e.ctrlKey) && e.key === "k") {
      e.preventDefault();
      // TODO: Implement search functionality
    }

    // Cmd/Ctrl + /: Show shortcuts
    if ((e.metaKey || e.ctrlKey) && e.key === "/") {
      e.preventDefault();
      alert(
        "Keyboard Shortcuts:\n\n" +
          "Enter: Send message\n" +
          "Shift+Enter: New line\n" +
          "Cmd/Ctrl+K: Search chats\n" +
          "Cmd/Ctrl+/: Show shortcuts"
      );
    }
  }

  // Storage
  function saveChats() {
    try {
      localStorage.setItem("starport_chats", JSON.stringify(state.chats));
    } catch (e) {
      console.error("Failed to save chats:", e);
      showError("Failed to save chats. Storage may be full.");
    }
  }

  function loadChats() {
    try {
      const saved = localStorage.getItem("starport_chats");
      if (saved) {
        state.chats = JSON.parse(saved);

        // Clean up any temporary chats that shouldn't have been saved
        Object.keys(state.chats).forEach((chatId) => {
          if (state.chats[chatId].temporary) {
            delete state.chats[chatId];
          }
        });
      }
    } catch (e) {
      console.error("Failed to load chats:", e);
      state.chats = {};
    }
  }

  // Utility Functions
  function generateId() {
    return Date.now().toString(36) + Math.random().toString(36).substr(2);
  }

  function groupChatsByTime(chats) {
    const now = new Date();
    const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
    const yesterday = new Date(today);
    yesterday.setDate(yesterday.getDate() - 1);
    const weekAgo = new Date(today);
    weekAgo.setDate(weekAgo.getDate() - 7);
    const monthAgo = new Date(today);
    monthAgo.setDate(monthAgo.getDate() - 30);

    const groups = {
      pinned: { label: "Pinned", chats: [] },
      today: { label: "Today", chats: [] },
      yesterday: { label: "Yesterday", chats: [] },
      previousWeek: { label: "Previous 7 Days", chats: [] },
      previousMonth: { label: "Previous 30 Days", chats: [] },
      older: { label: "Older", chats: [] },
    };

    chats.forEach((chat) => {
      // Check if chat is pinned
      if (state.pinnedChats.includes(chat.id)) {
        groups.pinned.chats.push(chat);
      } else {
        // Group by time for unpinned chats
        const chatDate = new Date(chat.lastModified);

        if (chatDate >= today) {
          groups.today.chats.push(chat);
        } else if (chatDate >= yesterday) {
          groups.yesterday.chats.push(chat);
        } else if (chatDate >= weekAgo) {
          groups.previousWeek.chats.push(chat);
        } else if (chatDate >= monthAgo) {
          groups.previousMonth.chats.push(chat);
        } else {
          groups.older.chats.push(chat);
        }
      }
    });

    return groups;
  }

  function formatDate(timestamp) {
    const date = new Date(timestamp);
    const now = new Date();
    const diff = now - date;

    if (diff < 60000) return "Just now";
    if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
    if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`;
    if (diff < 604800000) return `${Math.floor(diff / 86400000)}d ago`;

    return date.toLocaleDateString();
  }

  function escapeHtml(text) {
    const div = document.createElement("div");
    div.textContent = text;
    return div.innerHTML;
  }

  function formatMessageContent(content, isReasoning = false) {
    // Check if marked and DOMPurify are available
    if (typeof marked !== "undefined" && typeof DOMPurify !== "undefined") {
      // Configure marked options for better security and rendering
      marked.setOptions({
        breaks: true, // Convert line breaks to <br>
        gfm: true, // GitHub Flavored Markdown
        headerIds: false, // Disable header IDs for security
        mangle: false, // Don't mangle email addresses
        sanitize: false, // We'll use DOMPurify for sanitization
        highlight: function (code, lang) {
          // Use Prism.js if available
          if (typeof Prism !== "undefined" && lang) {
            // Map common language aliases to Prism language names
            const langMap = {
              js: "javascript",
              ts: "typescript",
              py: "python",
              rb: "ruby",
              yml: "yaml",
              sh: "bash",
              shell: "bash",
            };
            const prismLang = langMap[lang] || lang;

            // Check if language is loaded, if not let autoloader handle it
            if (Prism.languages[prismLang]) {
              try {
                return Prism.highlight(
                  code,
                  Prism.languages[prismLang],
                  prismLang
                );
              } catch (err) {
                console.error("Prism.js error:", err);
              }
            }
          }
          // Fall back to no highlighting but preserve the code for Prism autoloader
          return code;
        },
      });

      // Parse markdown with marked
      let rawHtml = marked.parse(content);

      // Pre-process Mermaid diagrams before DOMPurify
      // Replace mermaid code blocks with placeholder divs
      let mermaidCounter = 0;
      rawHtml = rawHtml.replace(
        /<pre><code class="language-mermaid">([\s\S]*?)<\/code><\/pre>/g,
        (match, diagram) => {
          const id = `mermaid-${Date.now()}-${mermaidCounter++}`;
          // Escape the diagram content for safe storage in data attribute
          const escapedDiagram = escapeHtml(diagram.trim());
          return `<div class="mermaid-container" data-mermaid-id="${id}" data-mermaid-content="${escapedDiagram}"><div class="mermaid-loading">Rendering diagram...</div></div>`;
        }
      );

      // Configure DOMPurify to allow safe HTML elements and attributes
      const cleanHtml = DOMPurify.sanitize(rawHtml, {
        ALLOWED_TAGS: [
          "p",
          "br",
          "strong",
          "em",
          "b",
          "i",
          "code",
          "pre",
          "blockquote",
          "ul",
          "ol",
          "li",
          "a",
          "h1",
          "h2",
          "h3",
          "h4",
          "h5",
          "h6",
          "hr",
          "table",
          "thead",
          "tbody",
          "tr",
          "th",
          "td",
          "del",
          "sup",
          "sub",
          // KaTeX elements
          "span",
          "div",
          "annotation",
          "semantics",
          "math",
          "mi",
          "mn",
          "mo",
          "ms",
          "mspace",
          "mtext",
          "mglyph",
          "mrow",
          "mfrac",
          "msqrt",
          "mroot",
          "msub",
          "msup",
          "msubsup",
          "munder",
          "mover",
          "munderover",
          "mmultiscripts",
          "mtable",
          "mtr",
          "mtd",
          "maligngroup",
          "malignmark",
          "maction",
          "merror",
          "mphantom",
          "mstyle",
          "menclose",
        ],
        ALLOWED_ATTR: [
          "href",
          "title",
          "target",
          "rel",
          "class",
          "style",
          "data-mermaid-id",
          "data-mermaid-content",
        ],
        ALLOW_DATA_ATTR: false,
        // Ensure external links open in new tab with security
        SAFE_FOR_TEMPLATES: true,
        ADD_ATTR: ["target", "rel"],
        FORBID_TAGS: ["style", "script", "iframe", "form", "input"],
        FORBID_ATTR: ["style", "onerror", "onload", "onclick"],
      });

      // Post-process to ensure external links are safe
      const tempDiv = document.createElement("div");
      tempDiv.innerHTML = cleanHtml;
      tempDiv.querySelectorAll("a").forEach((link) => {
        const href = link.getAttribute("href");
        if (
          href &&
          (href.startsWith("http://") || href.startsWith("https://"))
        ) {
          link.setAttribute("target", "_blank");
          link.setAttribute("rel", "noopener noreferrer");
        }
      });

      // Store the final HTML
      const finalHtml = tempDiv.innerHTML;

      return finalHtml;
    } else {
      // Fallback to basic formatting if libraries aren't loaded
      let formatted = escapeHtml(content);

      // Code blocks
      formatted = formatted.replace(/```([\s\S]*?)```/g, (match, code) => {
        return `<pre><code>${code.trim()}</code></pre>`;
      });

      // Inline code
      formatted = formatted.replace(/`([^`]+)`/g, "<code>$1</code>");

      // Bold
      formatted = formatted.replace(/\*\*(.*?)\*\*/g, "<strong>$1</strong>");

      // Italic
      formatted = formatted.replace(/\*(.*?)\*/g, "<em>$1</em>");

      // Line breaks
      formatted = formatted.replace(/\n/g, "<br>");

      // Paragraphs
      formatted = formatted.replace(/<br><br>/g, "</p><p>");
      formatted = "<p>" + formatted + "</p>";

      return formatted;
    }
  }

  function autoResizeTextarea(textarea) {
    textarea.style.height = "auto";
    textarea.style.height = Math.min(textarea.scrollHeight, 200) + "px";
  }

  function copyCodeBlock(codeElement, button) {
    const text = codeElement.textContent || "";

    navigator.clipboard
      .writeText(text)
      .then(() => {
        const originalText = button.textContent;
        button.textContent = "Copied!";
        button.classList.add("copied");

        setTimeout(() => {
          button.textContent = originalText;
          button.classList.remove("copied");
        }, 2000);
      })
      .catch((err) => {
        console.error("Failed to copy code:", err);
        button.textContent = "Failed";
        setTimeout(() => {
          button.textContent = "Copy";
        }, 2000);
      });
  }

  function renderMermaidDiagram(container, diagram) {
    if (!container || !diagram) return;

    try {
      // Generate unique ID for this render
      const svgId = `mermaid-svg-${Date.now()}-${Math.random()
        .toString(36)
        .substr(2, 9)}`;

      // Use mermaid.render with promise
      mermaid
        .render(svgId, diagram)
        .then((result) => {
          container.innerHTML = result.svg;
          container.classList.add("mermaid-rendered");
          container.classList.remove("mermaid-error");
        })
        .catch((err) => {
          console.error("Mermaid rendering error:", err);
          container.innerHTML = `<div class="mermaid-error">Failed to render diagram: ${escapeHtml(
            err.message || err.toString()
          )}</div>`;
          container.classList.add("mermaid-error");
        });
    } catch (err) {
      console.error("Mermaid error:", err);
      container.innerHTML = `<div class="mermaid-error">Failed to render diagram: ${escapeHtml(
        err.message || err.toString()
      )}</div>`;
      container.classList.add("mermaid-error");
    }
  }

  function processPendingMermaidDiagrams() {
    if (typeof mermaid === "undefined") {
      console.log("Mermaid not available yet");
      return;
    }

    // Find all unprocessed mermaid containers
    const containers = document.querySelectorAll(
      ".mermaid-container:not(.mermaid-rendered):not(.mermaid-error)"
    );

    containers.forEach((container) => {
      const diagramContent = container.getAttribute("data-mermaid-content");
      if (diagramContent) {
        // Unescape the diagram content
        const tempDiv = document.createElement("div");
        tempDiv.innerHTML = diagramContent;
        const diagram = tempDiv.textContent || tempDiv.innerText || "";

        renderMermaidDiagram(container, diagram);
      }
    });
  }

  function renderSpecialContent(element) {
    if (!element) return;

    // Render math content
    renderMathInMessage(element.closest(".message") || element);

    // Render code highlighting and diagrams
    renderCodeAndDiagrams(element);
  }

  function renderCodeAndDiagrams(element) {
    // Render Prism code highlighting
    if (typeof Prism !== "undefined") {
      const codeBlocks = element.querySelectorAll(
        'pre code[class*="language-"]:not([data-prism-highlighted])'
      );
      codeBlocks.forEach((block) => {
        Prism.highlightElement(block);
        block.setAttribute("data-prism-highlighted", "true");

        // Add copy button to code block
        const pre = block.parentElement;
        if (pre && !pre.querySelector(".code-copy-button")) {
          const copyButton = document.createElement("button");
          copyButton.className = "code-copy-button";
          copyButton.textContent = "Copy";
          copyButton.onclick = () => copyCodeBlock(block, copyButton);
          pre.appendChild(copyButton);
        }
      });
    }

    // Process any pending Mermaid diagrams
    processPendingMermaidDiagrams();
  }

  function renderMathInMessage(messageElement) {
    if (typeof renderMathInElement === "undefined" || !messageElement) return;

    try {
      renderMathInElement(messageElement, {
        delimiters: [
          { left: "$$", right: "$$", display: true },
          { left: "$", right: "$", display: false },
          { left: "\\(", right: "\\)", display: false },
          { left: "\\[", right: "\\]", display: true },
        ],
        throwOnError: false,
        errorColor: "#cc0000",
        strict: false,
        trust: false,
        macros: {
          "\\eqref": "\\href{#1}{}",
        },
      });
    } catch (err) {
      console.error("KaTeX rendering error:", err);
    }
  }

  function formatLatency(ms) {
    if (ms >= 1000) {
      return (ms / 1000).toFixed(3) + "s";
    }
    return ms + "ms";
  }

  // Global functions for inline handlers
  window.copyMessage = function (timestamp) {
    const chat = state.chats[state.currentChatId];
    const message = chat.messages.find(
      (m) => m.timestamp === parseInt(timestamp)
    );
    if (message) {
      // Copy only the message content
      navigator.clipboard
        .writeText(message.content)
        .then(() => {
          // Find the copy button and update it
          const messageEl = document.getElementById(
            `message-${message.timestamp}`
          );
          const copyBtn = messageEl?.querySelector(
            ".message-actions button:first-child"
          );
          if (copyBtn) {
            const originalText = copyBtn.textContent;
            copyBtn.textContent = "Copied!";
            copyBtn.style.color = "#10b981"; // Green color

            // Reset after 2 seconds
            setTimeout(() => {
              copyBtn.textContent = originalText;
              copyBtn.style.color = "";
            }, 2000);
          }
        })
        .catch((err) => {
          console.error("Failed to copy:", err);
          showError("Failed to copy to clipboard");
        });
    }
  };

  window.toggleReasoning = function (timestamp) {
    const ts = parseInt(timestamp);

    // Toggle state
    if (state.expandedReasoning.has(ts)) {
      state.expandedReasoning.delete(ts);
    } else {
      state.expandedReasoning.add(ts);
    }

    // Directly update the DOM for immediate response
    const messageEl = document.getElementById(`message-${ts}`);
    if (messageEl) {
      const toggleBtn = messageEl.querySelector(".reasoning-toggle");
      const reasoningContent = messageEl.querySelector(".reasoning-content");

      if (toggleBtn && reasoningContent) {
        const isExpanded = state.expandedReasoning.has(ts);
        // Keep the existing icon/text, just update expanded state
        reasoningContent.className = `reasoning-content ${
          isExpanded ? "expanded" : "collapsed"
        }`;
      }
    }

    // Save state
    saveChats();
  };

  window.regenerateMessage = async function (timestamp) {
    // Don't regenerate if already generating
    if (state.isGenerating) return;

    const chat = state.chats[state.currentChatId];
    const messageIndex = chat.messages.findIndex(
      (m) => m.timestamp === parseInt(timestamp)
    );

    if (messageIndex > 0) {
      // Verify the previous message is from user
      const previousUserMessage = chat.messages[messageIndex - 1];
      if (!previousUserMessage || previousUserMessage.role !== "user") return;

      // Remove the assistant message from expanded reasoning set
      const assistantMessage = chat.messages[messageIndex];
      if (assistantMessage) {
        state.expandedReasoning.delete(assistantMessage.timestamp);
      }

      // Remove this message and all following messages
      chat.messages = chat.messages.slice(0, messageIndex);
      saveChats();
      updateMessagesUI();

      // Create new assistant message
      const newAssistantMessage = {
        role: "assistant",
        content: "",
        reasoning: "",
        timestamp: Date.now(),
        streaming: true,
        startTime: performance.now(),
        firstTokenTime: null,
      };
      chat.messages.push(newAssistantMessage);

      // Append the assistant message element directly
      const messageEl = createMessageElement(
        newAssistantMessage,
        chat.messages.length - 1
      );
      elements.messages.appendChild(messageEl);

      // Scroll to bottom if auto-scroll is enabled
      if (state.autoScroll) {
        elements.messages.scrollTop = elements.messages.scrollHeight;
      }

      // Call the generate function
      await generate(newAssistantMessage);
    }
  };

  // Search functionality
  function openSearch() {
    elements.searchModal.classList.add("active");
    elements.searchInput.value = "";
    elements.searchInput.focus();
    elements.searchResults.innerHTML = "";
  }

  function closeSearch() {
    elements.searchModal.classList.remove("active");
    elements.searchInput.value = "";
    elements.searchResults.innerHTML = "";
  }

  function performSearch() {
    const query = elements.searchInput.value.trim().toLowerCase();

    if (!query) {
      elements.searchResults.innerHTML = "";
      return;
    }

    const results = [];

    // Search through all chats
    Object.values(state.chats).forEach((chat) => {
      // Search in chat title
      if (chat.title.toLowerCase().includes(query)) {
        results.push({
          chatId: chat.id,
          title: chat.title,
          type: "title",
          preview: chat.title,
          date:
            chat.messages.length > 0 ? chat.messages[0].timestamp : Date.now(),
        });
      }

      // Search in messages
      chat.messages.forEach((message, index) => {
        if (message.content && message.content.toLowerCase().includes(query)) {
          results.push({
            chatId: chat.id,
            title: chat.title,
            type: "message",
            preview: message.content,
            messageIndex: index,
            date: message.timestamp || Date.now(),
          });
        }
      });
    });

    displaySearchResults(results, query);
  }

  function displaySearchResults(results, query) {
    if (results.length === 0) {
      elements.searchResults.innerHTML =
        '<div class="search-no-results">No results found</div>';
      return;
    }

    // Sort results by date (newest first)
    results.sort((a, b) => b.date - a.date);

    // Create result elements
    elements.searchResults.innerHTML = results
      .map((result, index) => {
        const highlighted = highlightText(result.preview, query);
        const date = new Date(result.date).toLocaleDateString();

        return `
        <div class="search-result-item" data-index="${index}">
          <div class="search-result-title">${escapeHtml(result.title)}</div>
          <div class="search-result-preview">${highlighted}</div>
          <div class="search-result-date">${date}</div>
        </div>
      `;
      })
      .join("");

    // Add click handlers
    const resultItems = elements.searchResults.querySelectorAll(
      ".search-result-item"
    );
    resultItems.forEach((item, index) => {
      item.addEventListener("click", () => {
        const result = results[index];
        loadChat(result.chatId);
        closeSearch();

        // Scroll to specific message if it's a message result
        if (result.type === "message" && result.messageIndex !== undefined) {
          setTimeout(() => {
            const chat = state.chats[result.chatId];
            const messageEl = document.querySelector(
              `#message-${chat.messages[result.messageIndex].timestamp}`
            );
            if (messageEl) {
              messageEl.scrollIntoView({ behavior: "smooth", block: "center" });
              messageEl.classList.add("highlight");
              setTimeout(() => messageEl.classList.remove("highlight"), 2000);
            }
          }, 100);
        }
      });
    });
  }

  function highlightText(text, query) {
    const escaped = escapeHtml(text);
    const regex = new RegExp(`(${escapeRegExp(query)})`, "gi");
    return escaped.replace(regex, '<span class="search-highlight">$1</span>');
  }

  function escapeRegExp(string) {
    return string.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  }

  function handleSearchKeydown(e) {
    const results = elements.searchResults.querySelectorAll(
      ".search-result-item"
    );
    const selected = elements.searchResults.querySelector(
      ".search-result-item.selected"
    );
    let currentIndex = -1;

    if (selected) {
      currentIndex = Array.from(results).indexOf(selected);
    }

    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        if (currentIndex < results.length - 1) {
          if (selected) selected.classList.remove("selected");
          results[currentIndex + 1].classList.add("selected");
          results[currentIndex + 1].scrollIntoView({ block: "nearest" });
        }
        break;

      case "ArrowUp":
        e.preventDefault();
        if (currentIndex > 0) {
          if (selected) selected.classList.remove("selected");
          results[currentIndex - 1].classList.add("selected");
          results[currentIndex - 1].scrollIntoView({ block: "nearest" });
        }
        break;

      case "Enter":
        e.preventDefault();
        if (selected) {
          selected.click();
        }
        break;
    }
  }

  // Debounce helper
  function debounce(func, wait) {
    let timeout;
    return function executedFunction(...args) {
      const later = () => {
        clearTimeout(timeout);
        func(...args);
      };
      clearTimeout(timeout);
      timeout = setTimeout(later, wait);
    };
  }

  // Initialize on load
  init();

  // Ensure KaTeX and Mermaid run after they're loaded
  window.addEventListener("load", () => {
    // Re-render math if KaTeX is available
    if (typeof renderMathInElement !== "undefined") {
      const messageElements = document.querySelectorAll(".message");
      messageElements.forEach((msgEl) => {
        renderMathInMessage(msgEl);
      });
    }

    // Process any mermaid diagrams
    processPendingMermaidDiagrams();
  });
})();
