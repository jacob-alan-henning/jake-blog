let metricFirstLoad = true;

function setupTabs() {
    document.querySelectorAll('.tab-button').forEach(link => {
        link.addEventListener('click', function(e) {
            if (e.button === 0 && !e.ctrlKey && !e.shiftKey && !e.metaKey) {
                e.preventDefault();
                const tabId = this.dataset.tab;
                switchTab(tabId);
            }
        });
    });
}

function switchTab(tabId, updateHash = true) {
    document.querySelectorAll('.tab-content').forEach(tab => {
        tab.classList.remove('active');
    });
    document.querySelectorAll('.tab-button').forEach(button => {
        button.classList.remove('active');
    });
    
    document.getElementById(tabId).classList.add('active');
    document.querySelector(`[data-tab="${tabId}"]`).classList.add('active');
    
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
    const hash = window.location.hash.slice(1) || 'blog';     const tabMap = {
        'blog': 'tab1',
        'telemetry': 'tab2',
        'about': 'tab3'
    };
    const tabId = tabMap[hash] || 'tab1';     switchTab(tabId, false); }

window.addEventListener('load', handleHash);

document.addEventListener('DOMContentLoaded', setupTabs);

document.body.addEventListener("htmx:configRequest", (event) => {
  const triggeringEl = event.detail.elt;
  if (triggeringEl && triggeringEl.id === "metrics-list"){
    if (metricFirstLoad) {
      metricFirstLoad = false;
      return;
    }
    if (window.location.hash !== "#telemetry") {
      event.preventDefault();
    }
  }
});
