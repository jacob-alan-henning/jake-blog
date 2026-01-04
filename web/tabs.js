let metricFirstLoad = true;
let costFirstLoad = true;

function setupTabs() {
    document.querySelectorAll('.tab-button').forEach(button => {
        button.addEventListener('click', function(e) {
            if (e.button === 0 && !e.ctrlKey && !e.shiftKey && !e.metaKey) {
                e.preventDefault();
                const group = this.closest('.tab-group');
                const tabName = this.dataset.tab;
                switchTab(group, tabName);
                updateHash();
            }
        });
    });
}

function switchTab(group, tabName) {
    group.querySelectorAll(':scope > .tab-buttons > .tab-button').forEach(btn => {
        btn.classList.remove('active');
    });
    group.querySelectorAll(':scope > .tab-panel').forEach(panel => {
        panel.classList.remove('active');
    });

    const activeButton = group.querySelector(`:scope > .tab-buttons > [data-tab="${tabName}"]`);
    const activePanel = group.querySelector(`:scope > .tab-panel[data-tab="${tabName}"]`);

    if (activeButton) activeButton.classList.add('active');
    if (activePanel) activePanel.classList.add('active');
}

function updateHash() {
    const path = [];
    let group = document.querySelector('.tab-group[data-tab-group="main"]');

    while (group) {
        const activeButton = group.querySelector(':scope > .tab-buttons > .tab-button.active');
        if (!activeButton) break;

        path.push(activeButton.dataset.tab);

        const activePanel = group.querySelector(':scope > .tab-panel.active');
        group = activePanel ? activePanel.querySelector(':scope > .tab-group') : null;
    }

    window.location.hash = path.join('/');
}

function handleHash() {
    const hash = window.location.hash.slice(1);
    const segments = hash ? hash.split('/') : [];

    let group = document.querySelector('.tab-group[data-tab-group="main"]');
    let index = 0;

    while (group) {
        const tabName = segments[index] || group.querySelector(':scope > .tab-buttons > .tab-button')?.dataset.tab;
        if (!tabName) break;

        switchTab(group, tabName);

        const activePanel = group.querySelector(':scope > .tab-panel.active');
        group = activePanel ? activePanel.querySelector(':scope > .tab-group') : null;
        index++;
    }
}

window.addEventListener('load', handleHash);
window.addEventListener('hashchange', handleHash);
document.addEventListener('DOMContentLoaded', setupTabs);

document.body.addEventListener("htmx:configRequest", (event) => {
    const triggeringEl = event.detail.elt;
    if (triggeringEl && triggeringEl.id === "metrics-list") {
        if (metricFirstLoad) {
            metricFirstLoad = false;
            return;
        }
        if (window.location.hash !== "#telemetry/metrics" && window.location.hash !== "#telemetry") {
            event.preventDefault();
        }
    }
    if (triggeringEl && triggeringEl.id === "cost-table") {
        if (costFirstLoad) {
            costFirstLoad = false;
            return;
        }
        if (window.location.hash !== "#telemetry/cost") {
            event.preventDefault();
        }
    }
});
