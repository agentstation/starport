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
    favoriteModels: new Set(
      JSON.parse(localStorage.getItem("starport_favorite_models") || "[]")
    ), // Track favorite model IDs
    models: [], // All available models
    modelPricing: {}, // Model pricing lookup
    showAllModels: false, // Show all models in dropdown
    modelDropdownOpen: false, // Track dropdown state
    modelFilters: {
      providers: [],
      capabilities: []
    }, // Active filters
    expandedFavoritesOnly: false, // Filter to show only favorites in expanded view
    activeProviderFilters: new Set(), // Active provider filters in expanded view
    activeCapabilityFilters: new Set(), // Active capability filters in expanded view
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
    modelSelectButton: document.getElementById("model-select-button"),
    modelSelectValue: document.querySelector(".model-select-name"),
    modelDropdown: document.getElementById("model-dropdown"),
    modelDropdownContent: document.getElementById("model-dropdown-content"),
    modelSearchInput: document.getElementById("model-search-input"),
    showAllModelsBtn: document.getElementById("show-all-models"),
    filterModelsBtn: document.getElementById("filter-models"),
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

    // Model dropdown
    elements.modelSelectButton.addEventListener("click", toggleModelDropdown);
    elements.modelSearchInput.addEventListener("input", filterModels);
    elements.showAllModelsBtn.addEventListener("click", toggleShowAllModels);
    elements.filterModelsBtn.addEventListener("click", showFilterOptions);
    
    // Close dropdown on click outside
    document.addEventListener("click", (e) => {
      if (!e.target.closest(".model-dropdown-wrapper") && state.modelDropdownOpen) {
        closeModelDropdown();
      }
    });
    
    // Close dropdown on escape
    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape" && state.modelDropdownOpen) {
        closeModelDropdown();
      }
    });
    
    // Expanded modal event listeners
    const expandedModal = document.getElementById("model-expanded-modal");
    const expandedCloseBtn = expandedModal.querySelector(".model-expanded-close");
    const expandedSearchInput = document.getElementById("model-expanded-search");
    const expandedFavoritesToggle = document.getElementById("expanded-favorites-toggle");
    const expandedFilterBtn = document.getElementById("expanded-filter-btn");
    
    // Close expanded modal
    expandedCloseBtn.addEventListener("click", closeExpandedModal);
    
    // Close on escape key
    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape" && expandedModal.classList.contains("show")) {
        closeExpandedModal();
      }
    });
    
    // Search in expanded view
    expandedSearchInput.addEventListener("input", (e) => {
      const searchTerm = e.target.value.toLowerCase();
      filterExpandedModels(searchTerm);
    });
    
    // Toggle favorites in expanded view
    expandedFavoritesToggle.addEventListener("click", () => {
      state.expandedFavoritesOnly = !state.expandedFavoritesOnly;
      createExpandedModelView();
    });
    
    // Filter button in expanded view
    expandedFilterBtn.addEventListener("click", (e) => {
      e.stopPropagation();
      showExpandedFilterOptions();
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
        const firstUserMsg = chat.messages.find((m) => m.role === "user");
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
          model: "google/gemini-1.5-flash",
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
          document.title = `${title} - ${config.title || "Starport Chat"}`;
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
    
    // Get or create the messages container
    let container = elements.messages.querySelector('.messages-container');
    if (!container) {
      container = document.createElement('div');
      container.className = 'messages-container';
      elements.messages.innerHTML = '';
      elements.messages.appendChild(container);
    }
    
    container.appendChild(messageEl);

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
    
    // Check X-Cache header for cache status (for streaming responses)
    const cacheHeader = response.headers.get('X-Cache');
    if (cacheHeader) {
      message.cacheHit = cacheHeader === 'HIT';
      // Get cache age if it's a hit
      if (message.cacheHit) {
        const cacheAgeHeader = response.headers.get('X-Cache-Age');
        if (cacheAgeHeader) {
          message.cacheAge = parseInt(cacheAgeHeader, 10);
        }
      }
    }
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

            // Calculate tokens per second (only for non-cache responses)
            if (
              message.latency > 0 &&
              message.usage &&
              message.usage.total_tokens > 0 &&
              !message.cacheHit
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

            // Check for cache info (only if not already set from headers)
            if (parsed.cache_info && message.cacheHit === undefined) {
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
    
    // Check X-Cache header for cache status
    const cacheHeader = response.headers.get('X-Cache');
    if (cacheHeader) {
      message.cacheHit = cacheHeader === 'HIT';
      // Get cache age if it's a hit
      if (message.cacheHit) {
        const cacheAgeHeader = response.headers.get('X-Cache-Age');
        if (cacheAgeHeader) {
          message.cacheAge = parseInt(cacheAgeHeader, 10);
        }
      }
    } else {
      // Fallback to response body if header not present
      message.cacheHit = data.cache_info?.hit;
    }

    // Calculate tokens per second (only for non-cache responses)
    if (
      message.latency > 0 &&
      message.usage &&
      message.usage.total_tokens > 0 &&
      !message.cacheHit
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

    // Get or create the messages container
    let container = elements.messages.querySelector('.messages-container');
    if (!container) {
      container = document.createElement('div');
      container.className = 'messages-container';
      elements.messages.innerHTML = '';
      elements.messages.appendChild(container);
    }

    // Clear the container content
    container.innerHTML = "";

    if (chat.messages.length === 0) {
      container.innerHTML = `
                <div class="welcome-message">
                    <h2>Welcome to ${escapeHtml(
                      config.title || "Starport Chat"
                    )}</h2>
                    <p>Select a model and start chatting!</p>
                </div>
            `;
      return;
    }

    chat.messages.forEach((message, index) => {
      const messageEl = createMessageElement(message, index);
      container.appendChild(messageEl);
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
        textEl.innerHTML =
          '<div class="thinking-indicator">Thinking<span class="thinking-dots"></span></div>';
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
                                message.reasoningDurationEstimated ? "~" : ""
                              }${message.reasoningDuration.toFixed(3)}s</span>`
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
    // Only show TPS if not from cache (cache hits have unrealistic TPS)
    if (message.tokensPerSecond && !message.cacheHit) {
      metadataHtml += `<span class="latency-badge" title="Tokens per second (includes reasoning)">TPS: ${message.tokensPerSecond.toFixed(
        1
      )} tok/s</span>`;
    }
    if (message.cacheHit !== undefined) {
      metadataHtml += `<span class="cache-badge ${
        message.cacheHit ? "hit" : "miss"
      }" title="Whether this response was served from cache">${
        message.cacheHit ? "Cache: HIT" : "Cache: MISS"
      }</span>`;
      
      // Show cache age if available
      if (message.cacheHit && message.cacheAge !== undefined) {
        const ageText = formatCacheAge(message.cacheAge);
        if (ageText) {
          metadataHtml += `<span class="cache-age-badge" title="How long ago this response was cached">${ageText}</span>`;
        }
      }
      
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
    // Only show TPS if not from cache (cache hits have unrealistic TPS)
    if (message.tokensPerSecond && !message.cacheHit) {
      metadataHtml += `<span class="latency-badge" title="Tokens per second (includes reasoning)">TPS: ${message.tokensPerSecond.toFixed(
        1
      )} tok/s</span>`;
    }
    if (message.cacheHit !== undefined) {
      metadataHtml += `<span class="cache-badge ${
        message.cacheHit ? "hit" : "miss"
      }" title="Whether this response was served from cache">${
        message.cacheHit ? "Cache: HIT" : "Cache: MISS"
      }</span>`;
      
      // Show cache age if available
      if (message.cacheHit && message.cacheAge !== undefined) {
        const ageText = formatCacheAge(message.cacheAge);
        if (ageText) {
          metadataHtml += `<span class="cache-age-badge" title="How long ago this response was cached">${ageText}</span>`;
        }
      }
      
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
                    ${
                      message.role === "assistant" &&
                      message.streaming &&
                      !message.content &&
                      !message.reasoning
                        ? '<div class="thinking-indicator">Thinking<span class="thinking-dots"></span></div>'
                        : message.role === "user"
                        ? escapeHtml(message.content)
                        : formatMessageContent(message.content)
                    }
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
  
  function formatModelDisplayName(modelId) {
    const [provider, modelName] = modelId.split("/");
    
    // Helper function to capitalize words
    const capitalize = (str) => str.charAt(0).toUpperCase() + str.slice(1).toLowerCase();
    
    // Special formatting for different providers
    switch(provider) {
      case 'openai':
        // Handle GPT models
        if (modelName.startsWith('gpt-')) {
          const parts = modelName.split('-');
          if (parts[0] === 'gpt' && parts[1]) {
            // Format like "GPT-4" or "GPT-3.5 Turbo"
            let formatted = `GPT-${parts[1].toUpperCase()}`;
            if (parts.length > 2) {
              formatted += ' ' + parts.slice(2).map(p => capitalize(p)).join(' ');
            }
            return formatted;
          }
        } else if (modelName.startsWith('o1') || modelName.startsWith('o3')) {
          // Format O1/O3 models
          return modelName.toUpperCase().replace('-', ' ');
        }
        // Default formatting for other OpenAI models
        return modelName.split('-').map(p => capitalize(p)).join(' ');
        
      case 'anthropic':
        // Format Claude models
        if (modelName.startsWith('claude-')) {
          // Handle suffix like :beta, :thinking, etc.
          let suffix = '';
          let baseName = modelName;
          if (modelName.includes(':')) {
            const colonIndex = modelName.indexOf(':');
            baseName = modelName.substring(0, colonIndex);
            suffix = ' ' + capitalize(modelName.substring(colonIndex + 1));
          }
          
          const parts = baseName.split('-');
          // Start with 'Claude'
          let formatted = ['Claude'];
          
          // Add all remaining parts, handling version numbers specially
          for (let i = 1; i < parts.length; i++) {
            if (/^\d/.test(parts[i])) {
              // Check if we should combine with previous number (e.g., 3-5 should become 3.5)
              // Only combine if both numbers are 1-2 digits (version parts), not dates/long numbers
              if (i > 1 && /^\d{1,2}$/.test(parts[i-1]) && /^\d{1,2}$/.test(parts[i]) && formatted[formatted.length-1].match(/^\d/)) {
                // Combine with previous number using a dot
                formatted[formatted.length-1] += '.' + parts[i];
              } else {
                formatted.push(parts[i]);
              }
            } else {
              formatted.push(capitalize(parts[i]));
            }
          }
          
          return formatted.join(' ') + suffix;
        }
        return modelName.split('-').map(p => capitalize(p)).join(' ');
        
      case 'google-ai-studio':
      case 'google':
        // Format Gemini models
        if (modelName.startsWith('gemini-')) {
          return 'Gemini ' + modelName.substring(7).replace(/-/g, ' ').replace(/(\d+)\.(\d+)/, '$1.$2');
        } else if (modelName.startsWith('gemma-')) {
          return 'Gemma ' + modelName.substring(6).replace(/-/g, ' ');
        }
        return modelName.split('-').map(p => capitalize(p)).join(' ');
        
      case 'groq':
        // Format Groq models
        if (modelName.includes('llama')) {
          // Format Llama models
          const parts = modelName.split('-');
          return parts.map((p, i) => {
            if (p === 'llama' || p === 'llama3') return 'Llama';
            if (p.match(/^\d+b$/)) return p.toUpperCase();
            if (/^\d/.test(p)) return p; // Keep version numbers as-is
            return capitalize(p);
          }).join(' ');
        } else if (modelName.includes('mixtral')) {
          const parts = modelName.split('-');
          return parts.map(p => {
            if (p === 'mixtral') return 'Mixtral';
            if (p.match(/^\d+x\d+b$/)) return p.toUpperCase();
            if (/^\d/.test(p)) return p;
            return capitalize(p);
          }).join(' ');
        }
        return modelName.split('-').map(p => {
          if (/^\d/.test(p)) return p;
          return capitalize(p);
        }).join(' ');
        
      case 'mistral':
        // Format Mistral models
        if (modelName.startsWith('mistral-')) {
          return 'Mistral ' + modelName.substring(8).split('-').map(p => capitalize(p)).join(' ');
        }
        return modelName.split('-').map(p => capitalize(p)).join(' ');
        
      case 'ollama':
        // Format Ollama models - typically already well-formatted
        return modelName.split('-').map(p => capitalize(p)).join(' ');
        
      default:
        // Default formatting - capitalize each word but keep numbers as-is
        return modelName.split('-').map(p => {
          if (/^\d/.test(p)) return p; // Keep version numbers as-is
          return capitalize(p);
        }).join(' ');
    }
  }
  
  async function loadModels() {
    try {
      // If no API key is set, show a helpful message
      if (!state.apiKey) {
        updateModelDropdown([]);
        elements.modelSelectValue.textContent = "Set API key in settings first";
        return;
      }

      const response = await fetch(`${config.apiBaseURL}/api/v1/models`, {
        headers: { Authorization: `Bearer ${state.apiKey}` },
      });

      if (!response.ok) {
        if (response.status === 401) {
          updateModelDropdown([]);
          elements.modelSelectValue.textContent = "Invalid API key - check settings";
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

      updateModelDropdown(state.models);
      updateModelPricing();
      updateConversationStats();
    } catch (error) {
      console.error("Failed to load models:", error);
      updateModelDropdown([]);
      elements.modelSelectValue.textContent = "Error loading models";
    }
  }

  // Model Dropdown Functions
  function toggleModelDropdown() {
    if (state.modelDropdownOpen) {
      closeModelDropdown();
    } else {
      openModelDropdown();
    }
  }

  function openModelDropdown() {
    state.modelDropdownOpen = true;
    elements.modelDropdown.classList.remove("hidden");
    elements.modelSelectButton.setAttribute("aria-expanded", "true");
    elements.modelSelectButton.setAttribute("data-state", "open");
    elements.modelSearchInput.focus();
    elements.modelSearchInput.value = "";
    filterModels();
  }

  function closeModelDropdown() {
    state.modelDropdownOpen = false;
    elements.modelDropdown.classList.add("hidden");
    elements.modelSelectButton.setAttribute("aria-expanded", "false");
    elements.modelSelectButton.setAttribute("data-state", "closed");
  }

  function selectModel(modelId) {
    state.selectedModel = modelId;
    localStorage.setItem("starport_model", state.selectedModel);
    
    // Update the button text with formatted name
    const model = state.models.find(m => m.id === modelId);
    if (model) {
      elements.modelSelectValue.textContent = formatModelDisplayName(model.id);
    }
    
    closeModelDropdown();
    updateUI();
    updateModelPricing();
    updateConversationStats();
  }

  function toggleFavoriteModel(modelId) {
    if (state.favoriteModels.has(modelId)) {
      state.favoriteModels.delete(modelId);
    } else {
      state.favoriteModels.add(modelId);
    }
    localStorage.setItem("starport_favorite_models", JSON.stringify(Array.from(state.favoriteModels)));
    updateModelDropdown(state.models);
  }
  
  function toggleFavorite(modelId) {
    // Alias for toggleFavoriteModel
    toggleFavoriteModel(modelId);
  }

  function filterModels() {
    const searchTerm = elements.modelSearchInput.value.toLowerCase();
    
    // If there's a search term, show all models that match (including hidden ones)
    if (searchTerm) {
      // Temporarily disable the "show limited" restriction for search
      const originalShowAll = state.showAllModels;
      state.showAllModels = true;
      
      // Filter all models based on search term
      const filteredModels = state.models.filter(model => {
        const modelId = model.id.toLowerCase();
        const displayName = formatModelDisplayName(model.id).toLowerCase();
        
        // Check for exact match or partial match
        if (modelId.includes(searchTerm) || displayName.includes(searchTerm)) {
          return true;
        }
        
        // Check if search term is trying to match a model without version suffix
        // e.g., "claude-opus-4" should match "claude-opus-4-20250514"
        const searchParts = searchTerm.split('/');
        if (searchParts.length === 2) {
          const [searchProvider, searchModel] = searchParts;
          const [modelProvider, modelName] = model.id.toLowerCase().split('/');
          
          // Check if provider matches and model name starts with search model
          if (searchProvider === modelProvider && modelName.startsWith(searchModel)) {
            return true;
          }
        }
        
        return false;
      });
      
      // Rebuild the dropdown with filtered models
      updateModelDropdown(filteredModels);
      
      // Restore the original showAll state
      state.showAllModels = originalShowAll;
    } else {
      // If no search term, restore original view with filters
      updateModelDropdown(state.models);
    }
  }
  
  function filterModelsDOM() {
    // This is the old DOM-based filtering, kept for compatibility
    const searchTerm = elements.modelSearchInput.value.toLowerCase();
    const modelItems = elements.modelDropdownContent.querySelectorAll(".model-item");
    
    modelItems.forEach(item => {
      const modelId = item.dataset.modelId;
      const modelName = modelId.toLowerCase();
      if (modelName.includes(searchTerm)) {
        item.style.display = "";
      } else {
        item.style.display = "none";
      }
    });

    // Update section visibility
    const sections = elements.modelDropdownContent.querySelectorAll(".model-section");
    sections.forEach(section => {
      const visibleItems = section.querySelectorAll(".model-item:not([style*='display: none'])");
      if (visibleItems.length === 0) {
        section.style.display = "none";
      } else {
        section.style.display = "";
      }
    });
  }

  function toggleShowAllModels() {
    console.log("toggleShowAllModels called - opening full-screen modal");
    // Close the dropdown first
    closeModelDropdown();
    
    // Get the modal elements
    const modal = document.getElementById("model-expanded-modal");
    const modalBody = document.getElementById("model-expanded-body");
    const searchInput = document.getElementById("model-expanded-search");
    
    // Show the modal
    modal.classList.add("show");
    document.body.style.overflow = "hidden"; // Prevent background scrolling
    
    // Create the expanded model view
    createExpandedModelView();
    
    // Focus on search input
    setTimeout(() => searchInput.focus(), 100);
  }
  
  function createExpandedModelView() {
    const modalBody = document.getElementById("model-expanded-body");
    const searchInput = document.getElementById("model-expanded-search");
    const favoritesToggle = document.getElementById("expanded-favorites-toggle");
    
    // Clear previous content
    modalBody.innerHTML = "";
    searchInput.value = "";
    
    // Filter models based on current state
    let filteredModels = state.models;
    
    // Apply favorites filter if active
    if (state.expandedFavoritesOnly) {
      filteredModels = filteredModels.filter(model => state.favoriteModels.has(model.id));
    }
    
    // Apply provider and capability filters
    if (state.activeProviderFilters.size > 0) {
      filteredModels = filteredModels.filter(model => {
        const provider = getProviderFromModel(model.id);
        return state.activeProviderFilters.has(provider);
      });
    }
    
    if (state.activeCapabilityFilters.size > 0) {
      filteredModels = filteredModels.filter(model => {
        const capabilities = getModelCapabilities(model);
        return Array.from(state.activeCapabilityFilters).some(filter => 
          capabilities[filter]
        );
      });
    }
    
    // Group models by provider
    const modelsByProvider = {};
    filteredModels.forEach(model => {
      const provider = getProviderFromModel(model.id);
      if (!modelsByProvider[provider]) {
        modelsByProvider[provider] = [];
      }
      modelsByProvider[provider].push(model);
    });
    
    // Create sections for each provider
    Object.entries(modelsByProvider).forEach(([provider, models]) => {
      const section = document.createElement("div");
      section.className = "model-provider-section";
      section.innerHTML = `
        <h3 class="model-provider-title">
          ${getProviderIcon(provider)}
          <span>${provider}</span>
        </h3>
        <div class="model-cards-grid">
          ${models.map(model => createModelCard(model)).join("")}
        </div>
      `;
      
      modalBody.appendChild(section);
    });
    
    // Add click handlers to model cards
    modalBody.querySelectorAll(".model-card").forEach(card => {
      card.addEventListener("click", () => {
        const modelId = card.dataset.modelId;
        selectModel(modelId);
        closeExpandedModal();
      });
      
      // Add favorite toggle handler
      const favoriteBtn = card.querySelector(".model-card-favorite");
      if (favoriteBtn) {
        favoriteBtn.addEventListener("click", (e) => {
          e.stopPropagation();
          const modelId = card.dataset.modelId;
          toggleFavorite(modelId);
          // Update the button state
          const isFavorite = state.favoriteModels.has(modelId);
          favoriteBtn.classList.toggle("active", isFavorite);
        });
      }
    });
    
    // Update favorites toggle state
    updateFavoritesToggle();
    
    // Update filter button state
    const filterBtn = document.getElementById("expanded-filter-btn");
    if (state.activeProviderFilters.size > 0 || state.activeCapabilityFilters.size > 0) {
      filterBtn.classList.add("has-filters");
    } else {
      filterBtn.classList.remove("has-filters");
    }
  }
  
  function createModelCard(model) {
    const isFavorite = state.favoriteModels.has(model.id);
    const isSelected = model.id === state.selectedModel;
    const capabilities = getModelCapabilities(model);
    const pricing = model.pricing || {};
    
    return `
      <div class="model-card ${isSelected ? 'selected' : ''}" data-model-id="${model.id}">
        <div class="model-card-header">
          <h4 class="model-card-name">${model.name || model.id}</h4>
          <button class="model-card-favorite ${isFavorite ? 'active' : ''}" aria-label="Toggle favorite">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M12 2l3.09 6.26L22 9.27l-5 4.87 1.18 6.88L12 17.77l-6.18 3.25L7 14.14 2 9.27l6.91-1.01z"/>
            </svg>
          </button>
        </div>
        <div class="model-card-id">${model.id}</div>
        ${Object.entries(capabilities).filter(([_, has]) => has).length > 0 ? `
          <div class="model-card-capabilities">
            ${Object.entries(capabilities).filter(([cap, has]) => has).map(([cap]) => 
              `<span class="capability-badge">${cap}</span>`
            ).join("")}
          </div>
        ` : ''}
        ${model.context_length ? `
          <div class="model-card-context">Context: ${formatNumber(model.context_length)} tokens</div>
        ` : ''}
        ${pricing.prompt || pricing.completion ? `
          <div class="model-card-pricing">
            ${pricing.prompt ? `Input: $${pricing.prompt}/1K` : ''}
            ${pricing.prompt && pricing.completion ? ' • ' : ''}
            ${pricing.completion ? `Output: $${pricing.completion}/1K` : ''}
          </div>
        ` : ''}
      </div>
    `;
  }
  
  function closeExpandedModal() {
    const modal = document.getElementById("model-expanded-modal");
    modal.classList.remove("show");
    document.body.style.overflow = ""; // Restore scrolling
  }
  
  function updateFavoritesToggle() {
    const toggle = document.getElementById("expanded-favorites-toggle");
    const indicator = toggle.querySelector(".favorites-indicator");
    const text = toggle.querySelector("span");
    
    if (state.expandedFavoritesOnly) {
      toggle.classList.add("active");
      text.textContent = `Favorites (${state.favoriteModels.size})`;
    } else {
      toggle.classList.remove("active");
      text.textContent = "Favorites";
    }
    
    // Update indicator
    indicator.style.display = state.favoriteModels.size > 0 ? "block" : "none";
  }
  
  function filterExpandedModels(searchTerm) {
    const modalBody = document.getElementById("model-expanded-body");
    const sections = modalBody.querySelectorAll(".model-provider-section");
    
    sections.forEach(section => {
      const cards = section.querySelectorAll(".model-card");
      let hasVisibleCards = false;
      
      cards.forEach(card => {
        const modelId = card.dataset.modelId;
        const modelName = card.querySelector(".model-card-name").textContent.toLowerCase();
        const modelIdText = card.querySelector(".model-card-id").textContent.toLowerCase();
        
        const matches = modelName.includes(searchTerm) || modelIdText.includes(searchTerm);
        card.style.display = matches ? "" : "none";
        
        if (matches) hasVisibleCards = true;
      });
      
      // Hide section if no visible cards
      section.style.display = hasVisibleCards ? "" : "none";
    });
  }
  
  function showExpandedFilterOptions() {
    // Remove any existing filter dropdown
    const existingDropdown = document.querySelector(".expanded-filter-dropdown");
    if (existingDropdown) {
      existingDropdown.remove();
      return;
    }
    
    // Get all unique providers and capabilities from models
    const providers = new Set();
    const capabilities = new Set();
    
    state.models.forEach(model => {
      providers.add(getProviderFromModel(model.id));
      const modelCaps = getModelCapabilities(model);
      if (modelCaps.vision) capabilities.add("vision");
      if (modelCaps.reasoning) capabilities.add("reasoning");
      if (modelCaps.artifacts) capabilities.add("artifacts");
    });
    
    // Create filter dropdown
    const filterDropdown = document.createElement("div");
    filterDropdown.className = "expanded-filter-dropdown";
    filterDropdown.innerHTML = `
      <div class="filter-section">
        <h4>Providers</h4>
        ${Array.from(providers).map((provider, i) => `
          <label class="filter-option">
            <input type="checkbox" id="expanded-provider-${i}" name="expanded-provider-${i}" value="${provider}" data-filter-type="provider"
              ${state.activeProviderFilters.has(provider) ? 'checked' : ''}>
            <span>${provider}</span>
          </label>
        `).join("")}
      </div>
      <div class="filter-section">
        <h4>Capabilities</h4>
        ${Array.from(capabilities).map((capability, i) => `
          <label class="filter-option">
            <input type="checkbox" id="expanded-capability-${i}" name="expanded-capability-${i}" value="${capability}" data-filter-type="capability"
              ${state.activeCapabilityFilters.has(capability) ? 'checked' : ''}>
            <span>${capability}</span>
          </label>
        `).join("")}
      </div>
      <div class="filter-actions">
        <button class="btn-small btn-secondary" id="expanded-clear-filters">Clear</button>
        <button class="btn-small btn-primary" id="expanded-apply-filters">Apply</button>
      </div>
    `;
    
    // Position near the filter button
    const filterBtn = document.getElementById("expanded-filter-btn");
    const footer = filterBtn.closest(".model-expanded-footer");
    footer.appendChild(filterDropdown);
    
    // Position above the footer
    const btnRect = filterBtn.getBoundingClientRect();
    const footerRect = footer.getBoundingClientRect();
    filterDropdown.style.position = "absolute";
    filterDropdown.style.bottom = "60px";
    filterDropdown.style.right = "24px";
    
    // Add event listeners
    document.getElementById("expanded-clear-filters").addEventListener("click", () => {
      state.activeProviderFilters.clear();
      state.activeCapabilityFilters.clear();
      filterDropdown.remove();
      createExpandedModelView();
    });
    
    document.getElementById("expanded-apply-filters").addEventListener("click", () => {
      // Update filters based on checkboxes
      state.activeProviderFilters.clear();
      state.activeCapabilityFilters.clear();
      
      filterDropdown.querySelectorAll("input[type='checkbox']:checked").forEach(checkbox => {
        const value = checkbox.value;
        const filterType = checkbox.dataset.filterType;
        
        if (filterType === "provider") {
          state.activeProviderFilters.add(value);
        } else if (filterType === "capability") {
          state.activeCapabilityFilters.add(value);
        }
      });
      
      filterDropdown.remove();
      createExpandedModelView();
    });
    
    // Close on click outside
    setTimeout(() => {
      document.addEventListener("click", function closeFilter(e) {
        if (!filterDropdown.contains(e.target) && e.target.id !== "expanded-filter-btn") {
          filterDropdown.remove();
          document.removeEventListener("click", closeFilter);
        }
      });
    }, 0);
  }
  
  function formatNumber(num) {
    if (num >= 1000000) {
      return (num / 1000000).toFixed(1) + "M";
    } else if (num >= 1000) {
      return (num / 1000).toFixed(0) + "K";
    }
    return num.toString();
  }

  function showFilterOptions() {
    // Remove any existing filter dropdown
    const existingDropdown = document.querySelector(".filter-dropdown");
    if (existingDropdown) {
      existingDropdown.remove();
      return;
    }
    
    // Create filter dropdown
    const filterDropdown = document.createElement("div");
    filterDropdown.className = "filter-dropdown";
    filterDropdown.innerHTML = `
      <div class="filter-section">
        <div class="filter-header">Providers</div>
        <div class="filter-options" id="provider-filters"></div>
      </div>
      <div class="filter-section">
        <div class="filter-header">Capabilities</div>
        <div class="filter-options">
          <label class="filter-option">
            <input type="checkbox" id="filter-cap-vision" name="filter-cap-vision" value="vision" ${state.modelFilters.capabilities.includes("vision") ? "checked" : ""}>
            <span>👁️ Vision</span>
          </label>
          <label class="filter-option">
            <input type="checkbox" id="filter-cap-reasoning" name="filter-cap-reasoning" value="reasoning" ${state.modelFilters.capabilities.includes("reasoning") ? "checked" : ""}>
            <span>🧠 Reasoning</span>
          </label>
          <label class="filter-option">
            <input type="checkbox" id="filter-cap-artifacts" name="filter-cap-artifacts" value="artifacts" ${state.modelFilters.capabilities.includes("artifacts") ? "checked" : ""}>
            <span>📄 Function Calling</span>
          </label>
        </div>
      </div>
      <div class="filter-actions">
        <button class="btn btn-secondary btn-small" id="clear-filters">Clear All</button>
        <button class="btn btn-primary btn-small" id="apply-filters">Apply</button>
      </div>
    `;

    // Get unique providers
    const providers = [...new Set(state.models.map(m => m.id.split("/")[0]))].sort();
    const providerFilters = filterDropdown.querySelector("#provider-filters");
    
    providers.forEach((provider, index) => {
      const label = document.createElement("label");
      label.className = "filter-option";
      label.innerHTML = `
        <input type="checkbox" id="filter-provider-${index}" name="filter-provider-${index}" value="${provider}" ${state.modelFilters.providers.includes(provider) ? "checked" : ""}>
        <span>${provider.charAt(0).toUpperCase() + provider.slice(1)}</span>
      `;
      providerFilters.appendChild(label);
    });

    // Position the dropdown relative to the model dropdown
    filterDropdown.style.bottom = "44px"; // Position above the footer
    filterDropdown.style.right = "8px";
    
    // Add to dropdown
    elements.modelDropdown.appendChild(filterDropdown);

    // Handle filter actions
    const applyBtn = filterDropdown.querySelector("#apply-filters");
    const clearBtn = filterDropdown.querySelector("#clear-filters");
    
    applyBtn.onclick = () => {
      // Update provider filters
      state.modelFilters.providers = Array.from(filterDropdown.querySelectorAll('#provider-filters input:checked'))
        .map(input => input.value);
      
      // Update capability filters
      state.modelFilters.capabilities = Array.from(filterDropdown.querySelectorAll('.filter-section:nth-child(2) input:checked'))
        .map(input => input.value);
      
      // Update dropdown
      updateModelDropdown(state.models);
      filterDropdown.remove();
    };
    
    clearBtn.onclick = () => {
      state.modelFilters.providers = [];
      state.modelFilters.capabilities = [];
      updateModelDropdown(state.models);
      filterDropdown.remove();
    };

    // Close on click outside
    const closeHandler = (e) => {
      if (!filterDropdown.contains(e.target) && e.target !== elements.filterModelsBtn) {
        filterDropdown.remove();
        document.removeEventListener("click", closeHandler);
      }
    };
    setTimeout(() => document.addEventListener("click", closeHandler), 0);
  }

  function updateModelDropdown(models) {
    elements.modelDropdownContent.innerHTML = "";

    if (models.length === 0) {
      elements.modelDropdownContent.innerHTML = 
        '<div class="model-empty">No models available</div>';
      return;
    }

    // Apply filters
    let filteredModels = models;
    
    // Filter by providers
    if (state.modelFilters.providers.length > 0) {
      filteredModels = filteredModels.filter(model => {
        const provider = model.id.split("/")[0];
        return state.modelFilters.providers.includes(provider);
      });
    }
    
    // Filter by capabilities
    if (state.modelFilters.capabilities.length > 0) {
      filteredModels = filteredModels.filter(model => {
        const capabilities = getModelCapabilities(model);
        return state.modelFilters.capabilities.every(cap => capabilities[cap]);
      });
    }

    // Separate favorites and others
    const favoriteModels = filteredModels.filter(m => state.favoriteModels.has(m.id));
    const otherModels = filteredModels.filter(m => !state.favoriteModels.has(m.id));

    // Show counts if not showing all
    const totalModels = favoriteModels.length + otherModels.length;
    const displayedModels = state.showAllModels ? totalModels : Math.min(6, favoriteModels.length) + Math.min(10, otherModels.length);
    
    // Create favorites section if any
    if (favoriteModels.length > 0) {
      const favSection = createModelSection("Favorites", favoriteModels, true);
      elements.modelDropdownContent.appendChild(favSection);
    }

    // Create other models section
    if (otherModels.length > 0) {
      const othersSection = createModelSection("Others", otherModels, false);
      elements.modelDropdownContent.appendChild(othersSection);
    }

    // Update show all button visibility and text
    if (totalModels > 16) { // Only show if more than default display count
      elements.showAllModelsBtn.style.display = "flex";
      const hiddenCount = totalModels - displayedModels;
      if (!state.showAllModels && hiddenCount > 0) {
        elements.showAllModelsBtn.querySelector("span").textContent = `Show all (${hiddenCount} more)`;
      }
    } else {
      elements.showAllModelsBtn.style.display = "none";
    }

    // Show filter indicator if filters are active
    if (state.modelFilters.providers.length > 0 || state.modelFilters.capabilities.length > 0) {
      elements.filterModelsBtn.classList.add("active");
    } else {
      elements.filterModelsBtn.classList.remove("active");
    }

    // Update selected model display
    if (state.selectedModel) {
      const model = models.find(m => m.id === state.selectedModel);
      if (model) {
        elements.modelSelectValue.textContent = formatModelDisplayName(model.id);
      }
    } else if (filteredModels.length > 0) {
      // Select first model if none selected
      selectModel(filteredModels[0].id);
    }
  }

  function createModelSection(title, models, isFavorites) {
    const section = document.createElement("div");
    section.className = "model-section";
    
    const header = document.createElement("div");
    header.className = "model-section-header";
    header.textContent = title;
    section.appendChild(header);
    
    const grid = document.createElement("div");
    grid.className = isFavorites ? "model-grid" : "model-list";
    
    // Limit display if not showing all
    const displayModels = state.showAllModels ? models : models.slice(0, isFavorites ? 6 : 10);
    
    displayModels.forEach(model => {
      const item = createModelItem(model, isFavorites);
      grid.appendChild(item);
    });
    
    section.appendChild(grid);
    return section;
  }

  function createModelItem(model, isInFavorites) {
    const item = document.createElement("div");
    item.className = "model-item";
    item.dataset.modelId = model.id;
    
    const [provider, modelName] = model.id.split("/");
    
    // Model info container
    const modelInfo = document.createElement("div");
    modelInfo.className = "model-info";
    
    // Provider icon
    const providerIcon = createProviderIcon(provider);
    modelInfo.appendChild(providerIcon);
    
    // Model name
    const nameSpan = document.createElement("span");
    nameSpan.className = "model-name";
    nameSpan.textContent = modelName;
    modelInfo.appendChild(nameSpan);
    
    // Premium badge if applicable
    if (model.premium) {
      const premiumBadge = document.createElement("svg");
      premiumBadge.className = "model-premium";
      premiumBadge.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 3h12l4 6-10 13L2 9Z"/><path d="M11 3 8 9l4 13 4-13-3-6"/><path d="M2 9h20"/></svg>';
      modelInfo.appendChild(premiumBadge);
    }
    
    item.appendChild(modelInfo);
    
    // Capabilities container
    const capabilities = document.createElement("div");
    capabilities.className = "model-capabilities";
    
    // Add capability badges based on model architecture and features
    const modelCapabilities = getModelCapabilities(model);
    if (modelCapabilities.vision) {
      capabilities.appendChild(createCapabilityBadge("vision", "👁️"));
    }
    if (modelCapabilities.reasoning) {
      capabilities.appendChild(createCapabilityBadge("reasoning", "🧠"));
    }
    if (modelCapabilities.artifacts) {
      capabilities.appendChild(createCapabilityBadge("artifacts", "📄"));
    }
    
    // Favorite button
    const favoriteBtn = document.createElement("button");
    favoriteBtn.className = "model-favorite";
    favoriteBtn.innerHTML = state.favoriteModels.has(model.id) 
      ? '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M12 2l3.09 6.26L22 9.27l-5 4.87 1.18 6.88L12 17.77l-6.18 3.25L7 14.14 2 9.27l6.91-1.01L12 2z"/></svg>'
      : '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 2l3.09 6.26L22 9.27l-5 4.87 1.18 6.88L12 17.77l-6.18 3.25L7 14.14 2 9.27l6.91-1.01L12 2z"/></svg>';
    favoriteBtn.onclick = (e) => {
      e.stopPropagation();
      toggleFavoriteModel(model.id);
    };
    capabilities.appendChild(favoriteBtn);
    
    item.appendChild(capabilities);
    
    // Click handler
    item.onclick = () => selectModel(model.id);
    
    // Mark as selected
    if (model.id === state.selectedModel) {
      item.classList.add("selected");
    }
    
    // Mark as disabled if not available
    if (model.disabled) {
      item.classList.add("disabled");
      item.onclick = null;
    }
    
    return item;
  }

  function createProviderIcon(provider) {
    const icon = document.createElement("div");
    icon.className = "model-provider-icon";
    
    // Simple colored circles for now - can be replaced with actual SVG icons
    const colors = {
      openai: "#10a37f",
      anthropic: "#d97757",
      google: "#4285f4",
      groq: "#f55036",
      mistral: "#ff7000",
      ollama: "#000000",
      azure: "#0078d4",
      vertexai: "#4285f4"
    };
    
    icon.style.backgroundColor = colors[provider] || "#666";
    icon.textContent = provider.charAt(0).toUpperCase();
    
    return icon;
  }

  function createCapabilityBadge(type, emoji) {
    const badge = document.createElement("div");
    badge.className = `model-capability model-capability-${type}`;
    badge.textContent = emoji;
    badge.title = type.charAt(0).toUpperCase() + type.slice(1);
    return badge;
  }

  function getModelCapabilities(model) {
    const capabilities = {
      vision: false,
      reasoning: false,
      artifacts: false
    };

    // Check for vision capability
    if (model.architecture && model.architecture.input_modalities) {
      capabilities.vision = model.architecture.input_modalities.includes("image");
    }
    
    // Check model ID for known vision models
    const modelId = model.id.toLowerCase();
    if (modelId.includes("vision") || 
        modelId.includes("gpt-4o") || 
        modelId.includes("claude-3") ||
        modelId.includes("gemini") ||
        modelId.includes("llava")) {
      capabilities.vision = true;
    }

    // Check for reasoning capability (o1 models, etc)
    if (modelId.includes("o1") || 
        modelId.includes("o3") ||
        modelId.includes("reasoning") ||
        modelId.includes("deepseek-r1")) {
      capabilities.reasoning = true;
    }

    // Check for artifacts/function calling capability
    if (modelId.includes("gpt-4") || 
        modelId.includes("gpt-3.5") ||
        modelId.includes("claude") ||
        modelId.includes("mistral") ||
        (model.supported_parameters && model.supported_parameters.includes("tools"))) {
      capabilities.artifacts = true;
    }

    return capabilities;
  }
  
  function getProviderFromModel(modelId) {
    // Extract provider from model ID (e.g., "openai/gpt-4" -> "openai")
    const parts = modelId.split("/");
    return parts.length > 1 ? parts[0] : "unknown";
  }
  
  function getProviderIcon(provider) {
    const icons = {
      openai: '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M22.282 9.821a5.985 5.985 0 0 0-.516-4.91 6.046 6.046 0 0 0-6.51-2.9A6.065 6.065 0 0 0 4.981 4.18a5.985 5.985 0 0 0-3.998 2.9 6.046 6.046 0 0 0 .743 7.097 5.975 5.975 0 0 0 .51 4.911 6.051 6.051 0 0 0 6.515 2.9A5.985 5.985 0 0 0 13.26 24a6.056 6.056 0 0 0 5.772-4.206 5.99 5.99 0 0 0 3.997-2.9 6.056 6.056 0 0 0-.747-7.073zM13.26 22.43a4.476 4.476 0 0 1-2.876-1.04l.141-.081 4.779-2.758a.795.795 0 0 0 .392-.681v-6.737l2.02 1.168a.071.071 0 0 1 .038.052v5.583a4.504 4.504 0 0 1-4.494 4.494zM3.6 18.304a4.47 4.47 0 0 1-.535-3.014l.142.085 4.783 2.759a.771.771 0 0 0 .78 0l5.843-3.369v2.332a.08.08 0 0 1-.033.062L9.74 19.95a4.5 4.5 0 0 1-6.14-1.646zM2.34 7.896a4.485 4.485 0 0 1 2.366-1.973V11.6a.766.766 0 0 0 .388.676l5.815 3.355-2.02 1.168a.076.076 0 0 1-.071 0l-4.83-2.786A4.504 4.504 0 0 1 2.34 7.872zm16.597 3.855l-5.833-3.387L15.119 7.2a.076.076 0 0 1 .071 0l4.83 2.791a4.494 4.494 0 0 1-.676 8.105v-5.678a.79.79 0 0 0-.407-.667zm2.01-3.023l-.141-.085-4.774-2.782a.776.776 0 0 0-.785 0L9.409 9.23V6.897a.066.066 0 0 1 .028-.061l4.83-2.787a4.5 4.5 0 0 1 6.68 4.66zm-12.64 4.135l-2.02-1.164a.08.08 0 0 1-.038-.057V6.075a4.5 4.5 0 0 1 7.375-3.453l-.142.08L8.704 5.46a.795.795 0 0 0-.393.681v6.737zm1.097-2.365l2.602-1.5 2.607 1.5v2.999l-2.597 1.5-2.607-1.5z"/></svg>',
      anthropic: '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M12 2L2 7v10c0 5.55 3.84 10.74 9 12 5.16-1.26 9-6.45 9-12V7l-10-5z"/></svg>',
      google: '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z"/><path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z"/><path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z"/><path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z"/></svg>',
      "google-ai-studio": '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z"/><path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z"/><path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z"/><path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z"/></svg>',
      groq: '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm0 18c-4.41 0-8-3.59-8-8s3.59-8 8-8 8 3.59 8 8-3.59 8-8 8zm-1-13h2v6h-2zm0 8h2v2h-2z"/></svg>',
      mistral: '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/></svg>',
      ollama: '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm-1 17.93c-3.94-.49-7-3.85-7-7.93 0-.62.08-1.21.21-1.79L9 15v1c0 1.1.9 2 2 2v1.93zm6.9-2.54c-.26-.81-1-1.39-1.9-1.39h-1v-3c0-.55-.45-1-1-1H8v-2h2c.55 0 1-.45 1-1V7h2c1.1 0 2-.9 2-2v-.41c2.93 1.19 5 4.06 5 7.41 0 2.08-.8 3.97-2.1 5.39z"/></svg>',
      azure: '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M12 2L2 7v10c0 5.55 3.84 10.74 9 12 5.16-1.26 9-6.45 9-12V7l-10-5z"/></svg>',
      "azure-openai": '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M12 2L2 7v10c0 5.55 3.84 10.74 9 12 5.16-1.26 9-6.45 9-12V7l-10-5z"/></svg>'
    };
    return icons[provider] || '<svg viewBox="0 0 24 24" fill="currentColor"><circle cx="12" cy="12" r="10"/></svg>';
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

  function formatCacheAge(seconds) {
    if (!seconds || seconds < 0) return '';
    
    if (seconds < 60) {
      return `Cache-Age: ${seconds}s ago`;
    } else if (seconds < 3600) {
      const minutes = Math.floor(seconds / 60);
      return `Cache-Age: ${minutes}m ago`;
    } else if (seconds < 86400) {
      const hours = Math.floor(seconds / 3600);
      return `Cache-Age: ${hours}h ago`;
    } else {
      const days = Math.floor(seconds / 86400);
      return `Cache-Age: ${days}d ago`;
    }
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
      
      // Get or create the messages container
      let container = elements.messages.querySelector('.messages-container');
      if (!container) {
        container = document.createElement('div');
        container.className = 'messages-container';
        elements.messages.innerHTML = '';
        elements.messages.appendChild(container);
      }
      
      container.appendChild(messageEl);

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
