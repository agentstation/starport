// Starport ChatUI JavaScript Client
(function() {
    'use strict';

    // Configuration
    const config = window.STARPORT_CONFIG || {
        apiBaseURL: window.location.origin,
        allowKeyGen: false
    };

    // State
    const state = {
        currentChatId: null,
        chats: {},
        apiKey: localStorage.getItem('starport_api_key') || '',
        selectedModel: localStorage.getItem('starport_model') || '',
        streamEnabled: localStorage.getItem('starport_stream') !== 'false',
        typewriterEnabled: localStorage.getItem('starport_typewriter') !== 'false',
        typewriterSpeed: localStorage.getItem('starport_typewriter_speed') || 'normal',
        isGenerating: false,
        abortController: null,
        typewriterQueues: new Map() // Map of timestamp -> TypewriterQueue
    };

    // DOM Elements
    const elements = {
        // Header
        themeToggle: document.getElementById('theme-toggle'),
        settingsBtn: document.getElementById('settings-btn'),
        
        // Sidebar
        sidebar: document.getElementById('sidebar'),
        sidebarToggle: document.getElementById('sidebar-toggle'),
        newChatBtn: document.getElementById('new-chat'),
        chatList: document.getElementById('chat-list'),
        clearAllBtn: document.getElementById('clear-all'),
        
        // Chat
        modelSelect: document.getElementById('model-select'),
        modelPricing: document.getElementById('model-pricing'),
        messages: document.getElementById('messages'),
        messageInput: document.getElementById('message-input'),
        sendBtn: document.getElementById('send-btn'),
        stopBtn: document.getElementById('stop-btn'),
        tokenCount: document.getElementById('token-count'),
        costEstimate: document.getElementById('cost-estimate'),
        
        // Settings Modal
        settingsModal: document.getElementById('settings-modal'),
        apiKeyInput: document.getElementById('api-key'),
        apiBaseInput: document.getElementById('api-base'),
        streamEnabledInput: document.getElementById('stream-enabled'),
        typewriterEnabledInput: document.getElementById('typewriter-enabled'),
        typewriterSpeedSelect: document.getElementById('typewriter-speed'),
        generateKeyBtn: document.getElementById('generate-key'),
        
        // Error Toast
        errorToast: document.getElementById('error-toast'),
        toastMessage: document.querySelector('.toast-message'),
        toastDetails: document.querySelector('.toast-details'),
        errorJson: document.querySelector('.error-json')
    };

    // Typewriter effect for smooth chunk rendering
    class TypewriterQueue {
        constructor(messageTimestamp, updateCallback) {
            this.messageTimestamp = messageTimestamp;
            this.updateCallback = updateCallback;
            this.queue = [];
            this.currentText = '';
            this.isTyping = false;
            this.finished = false;
            
            // Speed settings based on user preference
            const speedSettings = {
                instant: { base: 0, min: 0, max: 0 },
                fast: { base: 1, min: 0.5, max: 3 },
                normal: { base: 3, min: 1, max: 5 },
                slow: { base: 8, min: 4, max: 15 }
            };
            
            const setting = speedSettings[state.typewriterSpeed] || speedSettings.normal;
            this.baseSpeed = setting.base;
            this.minSpeed = setting.min;
            this.maxSpeed = setting.max;
            this.speedupFactor = 0.95; // Speed increases as queue grows
            this.chunkDelay = 0; // ms delay between chunks
        }

        addChunk(text) {
            this.queue.push(text);
            if (!this.isTyping) {
                this.processQueue();
            }
        }

        async processQueue() {
            if (this.queue.length === 0 || this.isTyping) return;
            
            this.isTyping = true;
            
            while (this.queue.length > 0) {
                const chunk = this.queue.shift();
                await this.typeChunk(chunk);
                
                if (this.queue.length > 0 && this.chunkDelay > 0) {
                    await this.sleep(this.chunkDelay);
                }
            }
            
            this.isTyping = false;
            
            // If we're finished and no more chunks, remove cursor
            if (this.finished && this.queue.length === 0) {
                this.updateCallback(this.currentText, false);
            }
        }

        async typeChunk(text) {
            // Instant mode - just add the whole chunk at once
            if (this.baseSpeed === 0) {
                this.currentText += text;
                this.updateCallback(this.currentText, true);
                return;
            }
            
            // Calculate dynamic typing speed based on queue size
            const queuePressure = Math.min(this.queue.length / 5, 1); // 0-1 scale
            const speedMultiplier = 1 - (queuePressure * (1 - this.speedupFactor));
            const currentSpeed = Math.max(
                this.minSpeed,
                Math.min(this.maxSpeed, this.baseSpeed * speedMultiplier)
            );
            
            // Type characters in small batches for smoother performance
            const batchSize = queuePressure > 0.7 ? 5 : queuePressure > 0.3 ? 3 : 1;
            
            for (let i = 0; i < text.length; i += batchSize) {
                const batch = text.slice(i, i + batchSize);
                this.currentText += batch;
                this.updateCallback(this.currentText, true); // true = still streaming
                await this.sleep(currentSpeed * batch.length);
            }
        }

        async finish() {
            // Mark as finished but continue typing animation
            this.finished = true;
            
            // If still typing, let it complete naturally
            if (this.isTyping) {
                return;
            }
            
            // If there are queued chunks, process them
            if (this.queue.length > 0) {
                await this.processQueue();
            }
            
            // Final update to remove cursor
            this.updateCallback(this.currentText, false); // false = done streaming
        }

        sleep(ms) {
            return new Promise(resolve => setTimeout(resolve, ms));
        }

        destroy() {
            this.queue = [];
            this.isTyping = false;
        }
    }

    // Initialize
    function init() {
        loadChats();
        loadModels();
        setupEventListeners();
        updateUI();
        
        // Create new chat if no chats exist
        if (Object.keys(state.chats).length === 0) {
            createNewChat();
        } else {
            // Load the most recent chat
            const chatIds = Object.keys(state.chats).sort((a, b) => 
                state.chats[b].lastModified - state.chats[a].lastModified
            );
            loadChat(chatIds[0]);
        }
    }

    // Event Listeners
    function setupEventListeners() {
        // Theme toggle
        elements.themeToggle.addEventListener('click', toggleTheme);
        
        // Settings
        elements.settingsBtn.addEventListener('click', () => showModal(elements.settingsModal));
        elements.apiKeyInput.value = state.apiKey;
        elements.streamEnabledInput.checked = state.streamEnabled;
        elements.typewriterEnabledInput.checked = state.typewriterEnabled;
        elements.typewriterSpeedSelect.value = state.typewriterSpeed;
        
        elements.apiKeyInput.addEventListener('change', (e) => {
            const previousKey = state.apiKey;
            state.apiKey = e.target.value;
            localStorage.setItem('starport_api_key', state.apiKey);
            updateUI();
            
            // Reload models when API key changes
            if (state.apiKey !== previousKey) {
                loadModels();
                // Show feedback
                if (state.apiKey) {
                    showToast('API key saved successfully', 'success');
                }
            }
        });
        
        elements.streamEnabledInput.addEventListener('change', (e) => {
            state.streamEnabled = e.target.checked;
            localStorage.setItem('starport_stream', state.streamEnabled);
        });
        
        elements.typewriterEnabledInput.addEventListener('change', (e) => {
            state.typewriterEnabled = e.target.checked;
            localStorage.setItem('starport_typewriter', state.typewriterEnabled);
        });
        
        elements.typewriterSpeedSelect.addEventListener('change', (e) => {
            state.typewriterSpeed = e.target.value;
            localStorage.setItem('starport_typewriter_speed', state.typewriterSpeed);
        });
        
        if (elements.generateKeyBtn) {
            elements.generateKeyBtn.addEventListener('click', generateAPIKey);
        }
        
        // Modal close buttons
        document.querySelectorAll('.modal-close').forEach(btn => {
            btn.addEventListener('click', () => hideModal(elements.settingsModal));
        });
        
        // Sidebar
        elements.sidebarToggle.addEventListener('click', toggleSidebar);
        elements.newChatBtn.addEventListener('click', createNewChat);
        elements.clearAllBtn.addEventListener('click', clearAllChats);
        
        // Model selection
        elements.modelSelect.addEventListener('change', (e) => {
            state.selectedModel = e.target.value;
            localStorage.setItem('starport_model', state.selectedModel);
            updateUI();
            updateModelPricing();
            updateConversationStats();
        });
        
        // Message input
        elements.messageInput.addEventListener('input', () => {
            autoResizeTextarea(elements.messageInput);
            updateUI();
        });
        
        elements.messageInput.addEventListener('keydown', (e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                sendMessage();
            }
        });
        
        // Send/Stop buttons
        elements.sendBtn.addEventListener('click', sendMessage);
        elements.stopBtn.addEventListener('click', stopGeneration);
        
        // Error toast
        document.querySelector('.toast-close').addEventListener('click', hideError);
        elements.toastMessage.addEventListener('click', () => {
            elements.toastDetails.style.display = 
                elements.toastDetails.style.display === 'none' ? 'block' : 'none';
        });
        
        // Keyboard shortcuts
        document.addEventListener('keydown', handleKeyboardShortcuts);
    }

    // Theme Management
    function toggleTheme() {
        const html = document.documentElement;
        const currentTheme = html.getAttribute('data-theme');
        const newTheme = currentTheme === 'dark' ? 'light' : 'dark';
        html.setAttribute('data-theme', newTheme);
        localStorage.setItem('starport_theme', newTheme);
    }

    // Modal Management
    function showModal(modal) {
        modal.classList.add('active');
    }

    function hideModal(modal) {
        modal.classList.remove('active');
    }

    // Sidebar Management
    function toggleSidebar() {
        elements.sidebar.classList.toggle('active');
    }

    // Chat Management
    function createNewChat() {
        const chatId = generateId();
        const chat = {
            id: chatId,
            title: 'New Chat',
            messages: [],
            created: Date.now(),
            lastModified: Date.now()
        };
        
        state.chats[chatId] = chat;
        state.currentChatId = chatId;
        saveChats();
        updateChatList();
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

    function clearAllChats() {
        if (confirm('Are you sure you want to clear all chats? This cannot be undone.')) {
            state.chats = {};
            localStorage.removeItem('starport_chats');
            createNewChat();
        }
    }

    function updateChatTitle(chatId, firstMessage) {
        const chat = state.chats[chatId];
        if (chat && chat.messages.length === 1) {
            // Update title based on first message
            chat.title = firstMessage.substring(0, 50) + (firstMessage.length > 50 ? '...' : '');
            saveChats();
            updateChatList();
        }
    }

    // Message Handling
    async function sendMessage() {
        const message = elements.messageInput.value.trim();
        if (!message || !state.apiKey || !state.selectedModel || state.isGenerating) return;
        
        const chat = state.chats[state.currentChatId];
        if (!chat) return;
        
        // Add user message
        const userMessage = {
            role: 'user',
            content: message,
            timestamp: Date.now()
        };
        chat.messages.push(userMessage);
        
        // Clear input
        elements.messageInput.value = '';
        autoResizeTextarea(elements.messageInput);
        
        // Update UI
        updateMessagesUI();
        updateChatTitle(state.currentChatId, message);
        
        // Prepare assistant message
        const assistantMessage = {
            role: 'assistant',
            content: '',
            timestamp: Date.now(),
            streaming: true,
            thinking: true,
            startTime: performance.now(),
            firstTokenTime: null
        };
        chat.messages.push(assistantMessage);
        console.log('Created assistant message with timestamp:', assistantMessage.timestamp);
        
        // Create typewriter queue for this message if enabled
        if (state.typewriterEnabled && state.streamEnabled) {
            const typewriter = new TypewriterQueue(assistantMessage.timestamp, (text, isStreaming) => {
                assistantMessage.content = text;
                assistantMessage.streaming = isStreaming;
                updateMessageUI(assistantMessage);
            });
            state.typewriterQueues.set(assistantMessage.timestamp, typewriter);
        }
        
        // Render the empty assistant message container
        updateMessagesUI();
        
        // Start generation
        state.isGenerating = true;
        state.abortController = new AbortController();
        updateUI();
        
        try {
            const requestBody = {
                model: state.selectedModel,
                messages: chat.messages.slice(0, -1).map(m => ({
                    role: m.role,
                    content: m.content
                })),
                stream: state.streamEnabled
            };
            
            const startTime = performance.now();
            
            const response = await fetch(`${config.apiBaseURL}/v1/chat/completions`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'Authorization': `Bearer ${state.apiKey}`
                },
                body: JSON.stringify(requestBody),
                signal: state.abortController.signal
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
            if (error.name === 'AbortError') {
                // Stop typewriter and add stopped message
                const typewriter = state.typewriterQueues.get(assistantMessage.timestamp);
                if (typewriter) {
                    typewriter.destroy();
                    state.typewriterQueues.delete(assistantMessage.timestamp);
                }
                assistantMessage.content += ' [Generation stopped]';
                updateMessageUI(assistantMessage);
            } else {
                showError(error.message, error);
                chat.messages.pop(); // Remove failed assistant message
            }
        } finally {
            state.isGenerating = false;
            
            // Finish typewriter animation
            const typewriter = state.typewriterQueues.get(assistantMessage.timestamp);
            if (typewriter) {
                await typewriter.finish();
                state.typewriterQueues.delete(assistantMessage.timestamp);
            }
            
            assistantMessage.streaming = false;
            saveChats();
            updateUI();
            // Don't rebuild the entire UI, just update the final state
            updateMessageUI(assistantMessage);
            updateConversationStats();
        }
    }

    async function handleStreamingResponse(response, message, startTime) {
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        
        while (true) {
            const { done, value } = await reader.read();
            if (done) break;
            
            buffer += decoder.decode(value, { stream: true });
            const lines = buffer.split('\n');
            buffer = lines.pop() || '';
            
            for (const line of lines) {
                if (line.startsWith('data: ')) {
                    const data = line.slice(6);
                    if (data === '[DONE]') {
                        message.latency = Math.round(performance.now() - message.startTime);
                        return;
                    }
                    
                    try {
                        const parsed = JSON.parse(data);
                        const content = parsed.choices?.[0]?.delta?.content;
                        if (content) {
                            // Clear thinking state and record time to first token
                            if (message.thinking) {
                                message.thinking = false;
                                message.firstTokenTime = Math.round(performance.now() - message.startTime);
                            }
                            
                            if (state.typewriterEnabled) {
                                // Add chunk to typewriter queue
                                const typewriter = state.typewriterQueues.get(message.timestamp);
                                if (typewriter) {
                                    typewriter.addChunk(content);
                                } else {
                                    // Fallback if typewriter not found
                                    message.content += content;
                                    updateMessageUI(message);
                                }
                            } else {
                                // Direct update without typewriter effect
                                message.content += content;
                                updateMessageUI(message);
                            }
                        }
                        
                        // Update usage info
                        if (parsed.usage) {
                            message.usage = parsed.usage;
                            updateConversationStats();
                            // Update the previous user message to show prompt tokens
                            updatePreviousUserMessageTokens(message);
                        }
                        
                        // Check for cache info
                        if (parsed.cache_info) {
                            message.cacheHit = parsed.cache_info.hit;
                        }
                    } catch (e) {
                        console.error('Failed to parse SSE data:', e);
                    }
                }
            }
        }
    }

    async function handleNonStreamingResponse(response, message, startTime) {
        const data = await response.json();
        message.content = data.choices?.[0]?.message?.content || '';
        message.latency = Math.round(performance.now() - message.startTime);
        message.usage = data.usage;
        message.cacheHit = data.cache_info?.hit;
        message.thinking = false;
        
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
        const canSend = elements.messageInput.value.trim() && 
                       state.apiKey && 
                       state.selectedModel && 
                       !state.isGenerating;
        elements.sendBtn.disabled = !canSend;
        
        // Show/hide send vs stop button
        elements.sendBtn.style.display = state.isGenerating ? 'none' : 'block';
        elements.stopBtn.style.display = state.isGenerating ? 'block' : 'none';
    }

    function updateChatList() {
        elements.chatList.innerHTML = '';
        
        const sortedChats = Object.values(state.chats).sort((a, b) => 
            b.lastModified - a.lastModified
        );
        
        sortedChats.forEach(chat => {
            const chatItem = document.createElement('div');
            chatItem.className = 'chat-item' + (chat.id === state.currentChatId ? ' active' : '');
            chatItem.innerHTML = `
                <div class="chat-item-title">${escapeHtml(chat.title)}</div>
                <div class="chat-item-date">${formatDate(chat.lastModified)}</div>
            `;
            chatItem.addEventListener('click', () => loadChat(chat.id));
            elements.chatList.appendChild(chatItem);
        });
    }

    function updateMessagesUI() {
        const chat = state.chats[state.currentChatId];
        if (!chat) return;
        
        elements.messages.innerHTML = '';
        
        if (chat.messages.length === 0) {
            elements.messages.innerHTML = `
                <div class="welcome-message">
                    <h2>Welcome to ${escapeHtml(config.title || 'Starport Chat')}</h2>
                    <p>Select a model and start chatting!</p>
                </div>
            `;
            return;
        }
        
        chat.messages.forEach((message, index) => {
            const messageEl = createMessageElement(message, index);
            elements.messages.appendChild(messageEl);
        });
        
        // Scroll to bottom
        elements.messages.scrollTop = elements.messages.scrollHeight;
    }

    function updatePreviousUserMessageTokens(assistantMessage) {
        const chat = state.chats[state.currentChatId];
        if (!chat || !assistantMessage.usage) return;
        
        // Find the index of this assistant message
        const assistantIndex = chat.messages.findIndex(m => m.timestamp === assistantMessage.timestamp);
        if (assistantIndex <= 0) return;
        
        // Get the previous message (should be user message)
        const userMessage = chat.messages[assistantIndex - 1];
        if (userMessage.role !== 'user') return;
        
        // Find the user message element and update its metadata
        const messageElements = elements.messages.querySelectorAll('.message');
        const userMessageEl = messageElements[assistantIndex - 1];
        if (userMessageEl) {
            const metadataEl = userMessageEl.querySelector('.message-metadata');
            if (metadataEl) {
                metadataEl.innerHTML = `<span class="token-badge" title="Total prompt tokens">Tokens: ${assistantMessage.usage.prompt_tokens.toLocaleString()}</span>`;
            }
        }
    }
    
    function updateMessageUI(message) {
        // Get the current chat to find message index
        const chat = state.chats[state.currentChatId];
        if (!chat) return;
        
        // Find the message index
        const messageIndex = chat.messages.findIndex(m => m.timestamp === message.timestamp);
        if (messageIndex === -1) {
            console.error('Message not found in chat messages');
            return;
        }
        
        // Find the message element by index (more reliable than timestamp)
        const messageElements = elements.messages.querySelectorAll('.message');
        const messageEl = messageElements[messageIndex];
        
        if (messageEl) {
            const textEl = messageEl.querySelector('.message-text');
            if (textEl) {
                let fullHTML = '';
                if (message.thinking && !message.content) {
                    fullHTML = '<span class="thinking-indicator">Thinking...</span>';
                } else {
                    fullHTML = formatMessageContent(message.content);
                    
                    // If streaming, add cursor at the very end of the content
                    if (message.streaming && message.content) {
                        // Find the last closing </p> tag and insert cursor before it
                        const lastPIndex = fullHTML.lastIndexOf('</p>');
                        if (lastPIndex !== -1) {
                            fullHTML = fullHTML.slice(0, lastPIndex) + 
                                      '<span class="typing-indicator">▍</span>' + 
                                      fullHTML.slice(lastPIndex);
                        } else {
                            // No paragraph tags, append cursor at the end
                            fullHTML += '<span class="typing-indicator">▍</span>';
                        }
                    }
                }
                textEl.innerHTML = fullHTML;
                
                // Update metadata if message is complete (not streaming)
                if (!message.streaming) {
                    const metadataEl = messageEl.querySelector('.message-metadata');
                    if (metadataEl) {
                        let metadataHtml = '';
                        
                        if (message.role === 'assistant' && message.usage) {
                            // For assistant messages, show completion tokens
                            const completionTokens = message.usage.completion_tokens || 0;
                            metadataHtml += `<span class="token-badge" title="Tokens generated">Tokens: ${completionTokens.toLocaleString()}</span>`;
                        }
                        
                        if (message.firstTokenTime) {
                            metadataHtml += `<span class="latency-badge" title="Time to First Token (milliseconds)">TTFT: ${formatLatency(message.firstTokenTime)}</span>`;
                        }
                        if (message.latency) {
                            metadataHtml += `<span class="latency-badge" title="Total response latency (milliseconds)">Latency: ${formatLatency(message.latency)}</span>`;
                        }
                        if (message.cacheHit !== undefined) {
                            metadataHtml += `<span class="cache-badge ${message.cacheHit ? 'hit' : 'miss'}" title="Whether this response was served from cache">${message.cacheHit ? 'Cache Hit' : 'Cache Miss'}</span>`;
                        }
                        metadataEl.innerHTML = metadataHtml;
                    }
                }
                
                // Scroll to bottom to show new content
                elements.messages.scrollTop = elements.messages.scrollHeight;
            }
        }
    }

    function createMessageElement(message, index) {
        const div = document.createElement('div');
        div.className = `message ${message.role}`;
        div.setAttribute('data-timestamp', String(message.timestamp));
        div.setAttribute('data-message-index', String(index));
        
        const avatar = message.role === 'user' ? 'U' : 'A';
        const roleDisplay = message.role === 'user' ? 'You' : 'Assistant';
        
        let metadataHtml = '';
        
        // For user messages, show total prompt tokens from the next assistant message
        if (message.role === 'user') {
            const chat = state.chats[state.currentChatId];
            if (chat && index < chat.messages.length - 1) {
                const nextMessage = chat.messages[index + 1];
                if (nextMessage.role === 'assistant' && nextMessage.usage && nextMessage.usage.prompt_tokens) {
                    metadataHtml += `<span class="token-badge" title="Total prompt tokens">Tokens: ${nextMessage.usage.prompt_tokens.toLocaleString()}</span>`;
                }
            }
        } else if (message.role === 'assistant' && message.usage) {
            // For assistant messages, show completion tokens
            const completionTokens = message.usage.completion_tokens || 0;
            metadataHtml += `<span class="token-badge" title="Tokens generated">Tokens: ${completionTokens.toLocaleString()}</span>`;
        }
        
        if (message.firstTokenTime) {
            metadataHtml += `<span class="latency-badge" title="Time to First Token (milliseconds)">TTFT: ${formatLatency(message.firstTokenTime)}</span>`;
        }
        if (message.latency) {
            metadataHtml += `<span class="latency-badge" title="Total response latency (milliseconds)">Latency: ${formatLatency(message.latency)}</span>`;
        }
        if (message.cacheHit !== undefined) {
            metadataHtml += `<span class="cache-badge ${message.cacheHit ? 'hit' : 'miss'}" title="Whether this response was served from cache">${message.cacheHit ? 'Cache Hit' : 'Cache Miss'}</span>`;
        }
        
        div.innerHTML = `
            <div class="message-avatar">${avatar}</div>
            <div class="message-content">
                <div class="message-header">
                    <span class="message-role">${roleDisplay}</span>
                    <div class="message-metadata">${metadataHtml}</div>
                </div>
                <div class="message-text">
                    ${message.thinking && !message.content ? '<span class="thinking-indicator">Thinking...</span>' : 
                      (() => {
                          let html = formatMessageContent(message.content);
                          if (message.streaming && message.content) {
                              // Find the last closing </p> tag and insert cursor before it
                              const lastPIndex = html.lastIndexOf('</p>');
                              if (lastPIndex !== -1) {
                                  html = html.slice(0, lastPIndex) + 
                                        '<span class="typing-indicator">▍</span>' + 
                                        html.slice(lastPIndex);
                              } else {
                                  // No paragraph tags, append cursor at the end
                                  html += '<span class="typing-indicator">▍</span>';
                              }
                          }
                          return html;
                      })()
                    }
                </div>
                ${message.role === 'assistant' ? `
                <div class="message-actions">
                    <button class="btn btn-ghost" onclick="copyMessage('${message.timestamp}')">Copy</button>
                    <button class="btn btn-ghost" onclick="regenerateMessage('${message.timestamp}')">Regenerate</button>
                </div>
                ` : ''}
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
                elements.modelSelect.innerHTML = '<option value="">Set API key in settings first</option>';
                return;
            }
            
            const response = await fetch(`${config.apiBaseURL}/api/v1/models`, {
                headers: { 'Authorization': `Bearer ${state.apiKey}` }
            });
            
            if (!response.ok) {
                if (response.status === 401) {
                    updateModelSelect([]);
                    elements.modelSelect.innerHTML = '<option value="">Invalid API key - check settings</option>';
                    return;
                }
                throw new Error(`Failed to load models: ${response.status}`);
            }
            
            const data = await response.json();
            state.models = data.data || [];
            
            // Build pricing lookup
            state.modelPricing = {};
            state.models.forEach(model => {
                if (model.pricing) {
                    state.modelPricing[model.id] = model.pricing;
                }
            });
            
            updateModelSelect(state.models);
            updateModelPricing();
            updateConversationStats();
            
        } catch (error) {
            console.error('Failed to load models:', error);
            updateModelSelect([]);
            elements.modelSelect.innerHTML = '<option value="">Error loading models</option>';
        }
    }

    function updateModelSelect(models) {
        elements.modelSelect.innerHTML = '';
        
        if (models.length === 0) {
            elements.modelSelect.innerHTML = '<option value="">No models available</option>';
            return;
        }
        
        // Group models by provider
        const modelsByProvider = {};
        models.forEach(model => {
            const [provider] = model.id.split('/');
            if (!modelsByProvider[provider]) {
                modelsByProvider[provider] = [];
            }
            modelsByProvider[provider].push(model);
        });
        
        // Create optgroups
        Object.entries(modelsByProvider).forEach(([provider, providerModels]) => {
            const optgroup = document.createElement('optgroup');
            optgroup.label = provider.charAt(0).toUpperCase() + provider.slice(1);
            
            providerModels.forEach(model => {
                const option = document.createElement('option');
                option.value = model.id;
                option.textContent = model.id.split('/')[1];
                
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
            localStorage.setItem('starport_model', state.selectedModel);
        }
    }

    // API Key Generation
    async function generateAPIKey() {
        try {
            const response = await fetch(`${config.apiBaseURL}/chat/generate-key`, {
                method: 'POST'
            });
            
            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error || `HTTP ${response.status}`);
            }
            
            const data = await response.json();
            elements.apiKeyInput.value = data.key;
            state.apiKey = data.key;
            localStorage.setItem('starport_api_key', state.apiKey);
            updateUI();
            
            // Load models with the new key
            await loadModels();
            
            showToast('API key generated and saved successfully!', 'success');
            
            // Close the modal after a short delay
            setTimeout(() => {
                hideModal(elements.settingsModal);
            }, 1500);
            
        } catch (error) {
            showError('Failed to generate API key', error);
        }
    }


    function updateModelPricing() {
        const pricing = state.modelPricing && state.selectedModel ? state.modelPricing[state.selectedModel] : null;
        
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
                    return '0.00';
                }
            };
            
            elements.modelPricing.innerHTML = `
                <span>$${formatPrice(promptPricePerMillion)}/M↓</span>
                <span style="margin-left: 8px;">$${formatPrice(completionPricePerMillion)}/M↑</span>
            `;
        } else {
            elements.modelPricing.textContent = '';
        }
    }

    function updateConversationStats() {
        const chat = state.chats[state.currentChatId];
        if (!chat) {
            // Clear stats display when no chat
            elements.tokenCount.textContent = '↓ 0 ↑ 0';
            elements.costEstimate.textContent = '';
            return;
        }
        
        let totalPromptTokens = 0;
        let totalCompletionTokens = 0;
        let totalCost = 0;
        
        // Get current model pricing (may not be loaded yet)
        const pricing = state.modelPricing && state.selectedModel ? state.modelPricing[state.selectedModel] : null;
        
        // Calculate totals from all messages
        chat.messages.forEach(message => {
            if (message.usage) {
                totalPromptTokens += message.usage.prompt_tokens || 0;
                totalCompletionTokens += message.usage.completion_tokens || 0;
                
                // Calculate cost for this message if we have pricing
                if (pricing) {
                    // Pricing is stored per 1k tokens, so divide by 1000
                    const promptCost = (message.usage.prompt_tokens / 1000) * parseFloat(pricing.prompt);
                    const completionCost = (message.usage.completion_tokens / 1000) * parseFloat(pricing.completion);
                    totalCost += promptCost + completionCost;
                }
            }
        });
        
        // Update token count display
        const hasStreamingMessages = chat.messages.some(m => m.role === 'assistant' && !m.usage);
        elements.tokenCount.innerHTML = `
            <div style="display: flex; gap: 12px; align-items: center; font-size: 13px;">
                <span title="Prompt tokens">↓ ${totalPromptTokens.toLocaleString()} tk</span>
                <span title="Completion tokens">↑ ${totalCompletionTokens.toLocaleString()} tk</span>
                ${hasStreamingMessages && state.streamEnabled ? '<span style="font-size: 10px; color: var(--text-tertiary);" title="Token counts not available for streaming responses">*</span>' : ''}
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
            elements.costEstimate.textContent = '';
        }
    }

    // Error Handling
    function showError(message, error) {
        elements.toastMessage.textContent = message;
        
        if (error) {
            elements.errorJson.textContent = JSON.stringify(error, null, 2);
        }
        
        elements.errorToast.style.display = 'block';
        
        // Auto-hide after 10 seconds
        setTimeout(hideError, 10000);
    }

    function hideError() {
        elements.errorToast.style.display = 'none';
        elements.toastDetails.style.display = 'none';
    }

    // Toast notifications
    function showToast(message, type = 'info') {
        // For now, use the error toast for all notifications
        elements.toastMessage.textContent = message;
        elements.errorToast.className = `toast toast-${type}`;
        elements.errorToast.style.display = 'block';
        
        // Auto-hide after 3 seconds for success messages
        setTimeout(() => {
            elements.errorToast.style.display = 'none';
        }, 3000);
    }

    // Keyboard Shortcuts
    function handleKeyboardShortcuts(e) {
        // Cmd/Ctrl + K: Focus search
        if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
            e.preventDefault();
            // TODO: Implement search functionality
        }
        
        // Cmd/Ctrl + /: Show shortcuts
        if ((e.metaKey || e.ctrlKey) && e.key === '/') {
            e.preventDefault();
            alert('Keyboard Shortcuts:\n\n' +
                  'Enter: Send message\n' +
                  'Shift+Enter: New line\n' +
                  'Cmd/Ctrl+K: Search chats\n' +
                  'Cmd/Ctrl+/: Show shortcuts');
        }
    }

    // Storage
    function saveChats() {
        try {
            localStorage.setItem('starport_chats', JSON.stringify(state.chats));
        } catch (e) {
            console.error('Failed to save chats:', e);
            showError('Failed to save chats. Storage may be full.');
        }
    }

    function loadChats() {
        try {
            const saved = localStorage.getItem('starport_chats');
            if (saved) {
                state.chats = JSON.parse(saved);
            }
        } catch (e) {
            console.error('Failed to load chats:', e);
            state.chats = {};
        }
    }

    // Utility Functions
    function generateId() {
        return Date.now().toString(36) + Math.random().toString(36).substr(2);
    }

    function formatDate(timestamp) {
        const date = new Date(timestamp);
        const now = new Date();
        const diff = now - date;
        
        if (diff < 60000) return 'Just now';
        if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
        if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`;
        if (diff < 604800000) return `${Math.floor(diff / 86400000)}d ago`;
        
        return date.toLocaleDateString();
    }

    function escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    function formatMessageContent(content) {
        // Basic markdown-like formatting
        let formatted = escapeHtml(content);
        
        // Code blocks
        formatted = formatted.replace(/```([\s\S]*?)```/g, (match, code) => {
            return `<pre><code>${code.trim()}</code></pre>`;
        });
        
        // Inline code
        formatted = formatted.replace(/`([^`]+)`/g, '<code>$1</code>');
        
        // Bold
        formatted = formatted.replace(/\*\*(.*?)\*\*/g, '<strong>$1</strong>');
        
        // Italic
        formatted = formatted.replace(/\*(.*?)\*/g, '<em>$1</em>');
        
        // Line breaks
        formatted = formatted.replace(/\n/g, '<br>');
        
        // Paragraphs
        formatted = formatted.replace(/<br><br>/g, '</p><p>');
        formatted = '<p>' + formatted + '</p>';
        
        return formatted;
    }

    function autoResizeTextarea(textarea) {
        textarea.style.height = 'auto';
        textarea.style.height = Math.min(textarea.scrollHeight, 200) + 'px';
    }

    function formatLatency(ms) {
        if (ms >= 1000) {
            return (ms / 1000).toFixed(3) + 's';
        }
        return ms + 'ms';
    }

    // Global functions for inline handlers
    window.copyMessage = function(timestamp) {
        const chat = state.chats[state.currentChatId];
        const message = chat.messages.find(m => m.timestamp === parseInt(timestamp));
        if (message) {
            navigator.clipboard.writeText(message.content);
            // Could show a toast notification here
        }
    };

    window.regenerateMessage = function(timestamp) {
        const chat = state.chats[state.currentChatId];
        const messageIndex = chat.messages.findIndex(m => m.timestamp === parseInt(timestamp));
        
        if (messageIndex > 0) {
            // Remove this message and all following messages
            chat.messages = chat.messages.slice(0, messageIndex);
            saveChats();
            updateMessagesUI();
            
            // Resend the last user message
            const lastUserMessage = chat.messages[chat.messages.length - 1];
            if (lastUserMessage && lastUserMessage.role === 'user') {
                elements.messageInput.value = lastUserMessage.content;
                sendMessage();
            }
        }
    };

    // Initialize on load
    init();
})();