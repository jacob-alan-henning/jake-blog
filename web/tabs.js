// static/tabs.js
function setupTabs() {
    // Handle click events on tab links
    document.querySelectorAll('.tab-button').forEach(link => {
        link.addEventListener('click', function(e) {
            // Only handle direct clicks, let the browser handle ctrl+click, right-click, etc
            if (e.button === 0 && !e.ctrlKey && !e.shiftKey && !e.metaKey) {
                e.preventDefault();
                const tabId = this.dataset.tab;
                switchTab(tabId);
            }
        });
    });
}

function switchTab(tabId, updateHash = true) {
    // Remove active class from all tabs and buttons
    document.querySelectorAll('.tab-content').forEach(tab => {
        tab.classList.remove('active');
    });
    document.querySelectorAll('.tab-button').forEach(button => {
        button.classList.remove('active');
    });
    
    // Add active class to selected tab and its button
    document.getElementById(tabId).classList.add('active');
    document.querySelector(`[data-tab="${tabId}"]`).classList.add('active');
    
    // Update URL hash if needed
    if (updateHash) {
        const hashMap = {
            'tab1': 'blog',
            'tab2': 'telemetry',
            'tab3': 'about'
        };
        window.location.hash = hashMap[tabId];
    }
}

function handleHash() {
    const hash = window.location.hash.slice(1) || 'blog'; // Remove # and default to 'blog'
    const tabMap = {
        'blog': 'tab1',
        'telemetry': 'tab2',
        'about': 'tab3'
    };
    const tabId = tabMap[hash] || 'tab1'; // Default to tab1 if invalid hash
    switchTab(tabId, false); // Don't update hash since we're responding to a hash change
}

// Initialize and set up event listeners
window.addEventListener('hashchange', handleHash);
window.addEventListener('load', handleHash);
document.addEventListener('DOMContentLoaded', setupTabs);