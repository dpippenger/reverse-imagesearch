const uploadArea = document.getElementById('uploadArea');
const fileInput = document.getElementById('fileInput');
const previewContainer = document.getElementById('previewContainer');
const previewImage = document.getElementById('previewImage');
const fileName = document.getElementById('fileName');
const fileSize = document.getElementById('fileSize');
const clearBtn = document.getElementById('clearBtn');
const searchBtn = document.getElementById('searchBtn');
const stopBtn = document.getElementById('stopBtn');
const progressSection = document.getElementById('progressSection');
const progressBar = document.getElementById('progressBar');
const progressText = document.getElementById('progressText');
const resultsSection = document.getElementById('resultsSection');
const resultsGrid = document.getElementById('resultsGrid');
const resultsCount = document.getElementById('resultsCount');
const noResults = document.getElementById('noResults');
const threshold = document.getElementById('threshold');
const thresholdValue = document.getElementById('thresholdValue');

let selectedFile = null;
let eventSource = null;

threshold.addEventListener('input', () => {
    thresholdValue.textContent = threshold.value + '%';
});

uploadArea.addEventListener('click', () => fileInput.click());

uploadArea.addEventListener('dragover', (e) => {
    e.preventDefault();
    uploadArea.classList.add('dragover');
});

uploadArea.addEventListener('dragleave', () => {
    uploadArea.classList.remove('dragover');
});

uploadArea.addEventListener('drop', (e) => {
    e.preventDefault();
    uploadArea.classList.remove('dragover');
    const files = e.dataTransfer.files;
    if (files.length > 0) {
        handleFile(files[0]);
    }
});

fileInput.addEventListener('change', () => {
    if (fileInput.files.length > 0) {
        handleFile(fileInput.files[0]);
    }
});

function handleFile(file) {
    if (!file.type.startsWith('image/')) {
        alert('Please select an image file');
        return;
    }

    selectedFile = file;

    const reader = new FileReader();
    reader.onload = (e) => {
        previewImage.src = e.target.result;
        fileName.textContent = file.name;
        fileSize.textContent = formatBytes(file.size);
        previewContainer.classList.add('active');
        uploadArea.style.display = 'none';
        searchBtn.disabled = false;
    };
    reader.readAsDataURL(file);
}

function formatBytes(bytes) {
    if (bytes === 0) return '0 Bytes';
    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}

clearBtn.addEventListener('click', () => {
    selectedFile = null;
    fileInput.value = '';
    previewContainer.classList.remove('active');
    uploadArea.style.display = 'block';
    searchBtn.disabled = true;
});

searchBtn.addEventListener('click', startSearch);
stopBtn.addEventListener('click', stopSearch);

function startSearch() {
    if (!selectedFile) return;

    const formData = new FormData();
    formData.append('image', selectedFile);
    formData.append('dir', document.getElementById('searchDir').value);
    formData.append('threshold', threshold.value);
    formData.append('workers', document.getElementById('workers').value);
    formData.append('topN', document.getElementById('topN').value);

    resultsGrid.textContent = '';
    noResults.style.display = 'none';
    progressSection.classList.add('active');
    resultsSection.classList.add('active');
    searchBtn.style.display = 'none';
    stopBtn.style.display = 'inline-block';
    progressBar.style.width = '0%';
    progressText.textContent = 'Starting search...';
    resultsCount.textContent = '0 matches found';

    let matchCount = 0;
    let searchStartTime = null;
    let lastScanned = 0;

    fetch('/api/search', {
        method: 'POST',
        body: formData
    })
    .then(response => response.json())
    .then(data => {
        if (data.error) {
            progressText.textContent = 'Error: ' + data.error;
            return;
        }

        eventSource = new EventSource('/api/results/' + data.searchId);

        eventSource.onmessage = (event) => {
            const result = JSON.parse(event.data);

            if (result.error) {
                progressText.textContent = 'Error: ' + result.error;
                eventSource.close();
                return;
            }

            if (result.total > 0) {
                if (searchStartTime === null && result.scanned > 0) {
                    searchStartTime = Date.now();
                    lastScanned = result.scanned;
                }

                const percent = Math.round((result.scanned / result.total) * 100);
                progressBar.style.width = percent + '%';

                let etaText = '';
                if (searchStartTime !== null && result.scanned > lastScanned) {
                    const elapsedMs = Date.now() - searchStartTime;
                    const scannedSinceStart = result.scanned - lastScanned;
                    const imagesPerMs = scannedSinceStart / elapsedMs;
                    const remaining = result.total - result.scanned;

                    if (imagesPerMs > 0 && remaining > 0) {
                        const etaMs = remaining / imagesPerMs;
                        etaText = ' - ETA: ' + formatEta(etaMs);
                    }
                }

                progressText.textContent = 'Scanned ' + result.scanned + ' of ' + result.total + ' images' + etaText;
            }

            if (result.match && result.match.path) {
                matchCount++;
                resultsCount.textContent = matchCount + ' match' + (matchCount !== 1 ? 'es' : '') + ' found';
                addResultCard(result);
            }

            if (result.done) {
                eventSource.close();
                searchComplete(matchCount);
            }
        };

        eventSource.onerror = () => {
            eventSource.close();
            searchComplete(matchCount);
        };
    })
    .catch(err => {
        progressText.textContent = 'Error: ' + err.message;
        searchComplete(0);
    });
}

function stopSearch() {
    if (eventSource) {
        eventSource.close();
    }
    searchComplete(parseInt(resultsCount.textContent) || 0);
}

function searchComplete(matchCount) {
    searchBtn.style.display = 'inline-block';
    stopBtn.style.display = 'none';
    progressText.textContent = 'Search complete';
    progressBar.style.width = '100%';

    if (matchCount === 0) {
        noResults.style.display = 'block';
    }
}

function addResultCard(result) {
    const card = document.createElement('div');
    card.className = 'result-card';

    const imgSrc = result.thumbnail
        ? 'data:image/jpeg;base64,' + result.thumbnail
        : '/api/thumbnail?path=' + encodeURIComponent(result.match.path);

    const infoId = 'exif-' + Date.now() + '-' + Math.random().toString(36).substr(2, 9);
    const downloadUrl = '/api/download?path=' + encodeURIComponent(result.match.path);

    const img = document.createElement('img');
    img.className = 'result-image';
    img.src = imgSrc;
    img.alt = 'Match';
    img.loading = 'lazy';
    card.appendChild(img);

    const info = document.createElement('div');
    info.className = 'result-info';

    const header = document.createElement('div');
    header.className = 'result-header';

    const similarity = document.createElement('span');
    similarity.className = 'result-similarity';
    similarity.textContent = result.match.similarity.toFixed(1) + '%';
    header.appendChild(similarity);

    const infoWrapper = document.createElement('div');
    infoWrapper.className = 'info-wrapper';

    const infoBtn = document.createElement('span');
    infoBtn.className = 'info-btn';
    infoBtn.textContent = 'i';
    infoBtn.dataset.path = result.match.path;
    infoBtn.dataset.tooltipId = infoId;

    const tooltip = document.createElement('div');
    tooltip.className = 'exif-tooltip';
    tooltip.id = infoId;
    tooltip.textContent = 'Loading...';

    infoWrapper.appendChild(infoBtn);
    infoWrapper.appendChild(tooltip);
    header.appendChild(infoWrapper);

    const downloadBtn = document.createElement('a');
    downloadBtn.className = 'download-btn';
    downloadBtn.href = downloadUrl;
    downloadBtn.title = 'Download original';
    downloadBtn.textContent = '\u2193';
    header.appendChild(downloadBtn);

    info.appendChild(header);

    const path = document.createElement('p');
    path.className = 'result-path';
    path.textContent = result.match.path;
    info.appendChild(path);

    card.appendChild(info);

    let exifLoaded = false;
    infoBtn.addEventListener('mouseenter', function() {
        if (exifLoaded) return;
        exifLoaded = true;

        const pathVal = this.dataset.path;
        const tooltipEl = document.getElementById(this.dataset.tooltipId);

        fetch('/api/exif?path=' + encodeURIComponent(pathVal))
            .then(response => response.json())
            .then(data => {
                renderExifTooltip(tooltipEl, data);
            })
            .catch(() => {
                tooltipEl.textContent = 'Failed to load EXIF data';
                tooltipEl.className = 'exif-tooltip exif-error';
            });
    });

    resultsGrid.appendChild(card);
}

function renderExifTooltip(tooltipEl, data) {
    tooltipEl.textContent = '';

    if (data.error && !data.width && !data.fileSize) {
        tooltipEl.textContent = data.error;
        tooltipEl.className = 'exif-tooltip exif-error';
        return;
    }

    const title = document.createElement('h4');
    title.textContent = 'Image Info';
    tooltipEl.appendChild(title);

    const fields = [
        ['File Size', data.fileSize ? formatBytes(data.fileSize) : null],
        ['Dimensions', data.width && data.height ? data.width + ' x ' + data.height : null],
        ['Camera', data.make && data.model ? data.make + ' ' + data.model : (data.model || data.make)],
        ['Date', data.dateTime],
        ['Aperture', data.fNumber],
        ['Shutter', data.exposureTime],
        ['ISO', data.iso],
        ['Focal Length', data.focalLength],
        ['Lens', data.lensModel],
        ['Orientation', data.orientation],
        ['Software', data.software],
        ['GPS', data.gpsLatitude && data.gpsLongitude ? data.gpsLatitude + ', ' + data.gpsLongitude : null]
    ];

    let hasData = false;
    for (const [label, value] of fields) {
        if (value) {
            const row = document.createElement('div');
            row.className = 'exif-row';

            const labelEl = document.createElement('span');
            labelEl.className = 'exif-label';
            labelEl.textContent = label;

            const valueEl = document.createElement('span');
            valueEl.className = 'exif-value';
            valueEl.textContent = value;

            row.appendChild(labelEl);
            row.appendChild(valueEl);
            tooltipEl.appendChild(row);
            hasData = true;
        }
    }

    if (!hasData) {
        const noData = document.createElement('div');
        noData.className = 'exif-error';
        noData.textContent = 'No metadata available';
        tooltipEl.appendChild(noData);
    }
}

function formatEta(ms) {
    const seconds = Math.ceil(ms / 1000);
    if (seconds < 60) {
        return seconds + 's';
    }
    const minutes = Math.floor(seconds / 60);
    const remainingSeconds = seconds % 60;
    if (minutes < 60) {
        return minutes + 'm ' + remainingSeconds + 's';
    }
    const hours = Math.floor(minutes / 60);
    const remainingMinutes = minutes % 60;
    return hours + 'h ' + remainingMinutes + 'm';
}

// Directory Browser
const browseBtn = document.getElementById('browseBtn');
const browserModal = document.getElementById('browserModal');
const modalClose = document.getElementById('modalClose');
const modalCancelBtn = document.getElementById('modalCancelBtn');
const modalSelectBtn = document.getElementById('modalSelectBtn');
const modalPathInput = document.getElementById('modalPathInput');
const modalGoBtn = document.getElementById('modalGoBtn');
const browserBody = document.getElementById('browserBody');
const searchDirInput = document.getElementById('searchDir');

let currentBrowsePath = '';
let selectedPath = '';
let browserTargetInput = null; // Which input to update when a directory is selected

browseBtn.addEventListener('click', () => {
    browserTargetInput = searchDirInput;
    openBrowser(searchDirInput.value || '');
});

modalClose.addEventListener('click', closeBrowser);
modalCancelBtn.addEventListener('click', closeBrowser);

browserModal.addEventListener('click', (e) => {
    if (e.target === browserModal) {
        closeBrowser();
    }
});

modalGoBtn.addEventListener('click', () => {
    const path = modalPathInput.value.trim();
    if (path) {
        loadDirectory(path);
    }
});

modalPathInput.addEventListener('keypress', (e) => {
    if (e.key === 'Enter') {
        const path = modalPathInput.value.trim();
        if (path) {
            loadDirectory(path);
        }
    }
});

modalSelectBtn.addEventListener('click', () => {
    if (selectedPath && browserTargetInput) {
        browserTargetInput.value = selectedPath;
        closeBrowser();
    }
});

function openBrowser(startPath) {
    browserModal.classList.add('active');
    selectedPath = '';
    loadDirectory(startPath);
}

function closeBrowser() {
    browserModal.classList.remove('active');
}

function loadDirectory(path) {
    browserBody.textContent = 'Loading...';

    const url = '/api/browse' + (path ? '?path=' + encodeURIComponent(path) : '');

    fetch(url)
        .then(response => response.json())
        .then(data => {
            if (data.error) {
                browserBody.textContent = data.error;
                browserBody.className = 'browser-error';
                return;
            }

            currentBrowsePath = data.path;
            selectedPath = data.path;
            modalPathInput.value = data.path;

            renderDirectory(data);
        })
        .catch(() => {
            browserBody.textContent = 'Failed to load directory';
            browserBody.className = 'browser-error';
        });
}

function renderDirectory(data) {
    browserBody.textContent = '';
    browserBody.className = '';

    const list = document.createElement('ul');
    list.className = 'browser-list';

    if (data.parent) {
        const parentItem = document.createElement('li');
        parentItem.className = 'browser-item parent-dir';
        parentItem.dataset.path = data.parent;
        parentItem.dataset.isdir = 'true';

        const icon = document.createElement('span');
        icon.className = 'browser-item-icon';
        icon.textContent = '📁';

        const name = document.createElement('span');
        name.className = 'browser-item-name';
        name.textContent = '..';

        parentItem.appendChild(icon);
        parentItem.appendChild(name);
        list.appendChild(parentItem);
    }

    for (const entry of data.entries) {
        const item = document.createElement('li');
        item.className = 'browser-item' + (entry.isDir ? '' : ' file-item');
        item.dataset.path = entry.path;
        item.dataset.isdir = String(entry.isDir);

        const icon = document.createElement('span');
        icon.className = 'browser-item-icon';
        icon.textContent = entry.isDir ? '📁' : '📄';

        const name = document.createElement('span');
        name.className = 'browser-item-name';
        name.textContent = entry.name;

        item.appendChild(icon);
        item.appendChild(name);
        list.appendChild(item);
    }

    browserBody.appendChild(list);

    const items = browserBody.querySelectorAll('.browser-item');
    items.forEach(item => {
        item.addEventListener('click', () => {
            const path = item.dataset.path;
            const isDir = item.dataset.isdir === 'true';

            if (isDir) {
                loadDirectory(path);
            }
        });

        item.addEventListener('dblclick', () => {
            const path = item.dataset.path;
            const isDir = item.dataset.isdir === 'true';

            if (isDir && browserTargetInput) {
                selectedPath = path;
                browserTargetInput.value = selectedPath;
                closeBrowser();
            }
        });
    });
}

document.addEventListener('keydown', (e) => {
    if (browserModal.classList.contains('active')) {
        if (e.key === 'Escape') {
            closeBrowser();
        }
    }
});

// Tab Navigation
const tabBtns = document.querySelectorAll('.tab-btn');
const tabContents = document.querySelectorAll('.tab-content');

tabBtns.forEach(btn => {
    btn.addEventListener('click', () => {
        const tabId = btn.dataset.tab;

        tabBtns.forEach(b => b.classList.remove('active'));
        tabContents.forEach(c => c.classList.remove('active'));

        btn.classList.add('active');
        document.getElementById(tabId + 'Tab').classList.add('active');

        if (tabId === 'settings') {
            loadCacheStats();
            loadCachedDirectories();
        }
    });
});

// Cache Settings
const cacheEnabled = document.getElementById('cacheEnabled');
const cacheDisabled = document.getElementById('cacheDisabled');
const statEntries = document.getElementById('statEntries');
const statHitRate = document.getElementById('statHitRate');
const statSize = document.getElementById('statSize');
const statHits = document.getElementById('statHits');
const scanDirInput = document.getElementById('scanDir');
const scanBrowseBtn = document.getElementById('scanBrowseBtn');
const scanBtn = document.getElementById('scanBtn');
const scanProgress = document.getElementById('scanProgress');
const scanProgressBar = document.getElementById('scanProgressBar');
const scanProgressText = document.getElementById('scanProgressText');
const refreshStatsBtn = document.getElementById('refreshStatsBtn');
const clearCacheBtn = document.getElementById('clearCacheBtn');

function loadCacheStats() {
    fetch('/api/cache/stats')
        .then(response => response.json())
        .then(data => {
            if (!data.enabled) {
                cacheEnabled.style.display = 'none';
                cacheDisabled.style.display = 'block';
                return;
            }

            cacheEnabled.style.display = 'block';
            cacheDisabled.style.display = 'none';

            statEntries.textContent = formatNumber(data.entries);
            statHitRate.textContent = data.hitRate.toFixed(1) + '%';
            statSize.textContent = data.sizeMB.toFixed(2) + ' MB';
            statHits.textContent = formatNumber(data.hits);
        })
        .catch(() => {
            cacheEnabled.style.display = 'none';
            cacheDisabled.style.display = 'block';
        });
}

function formatNumber(n) {
    if (n >= 1000000) {
        return (n / 1000000).toFixed(1) + 'M';
    }
    if (n >= 1000) {
        return (n / 1000).toFixed(1) + 'K';
    }
    return String(n);
}

refreshStatsBtn.addEventListener('click', loadCacheStats);

// Cached Directories
const cachedDirsContainer = document.getElementById('cachedDirsContainer');
const refreshDirsBtn = document.getElementById('refreshDirsBtn');

function loadCachedDirectories() {
    cachedDirsContainer.textContent = '';
    const loading = document.createElement('div');
    loading.className = 'cached-dirs-loading';
    loading.textContent = 'Loading...';
    cachedDirsContainer.appendChild(loading);

    fetch('/api/cache/directories')
        .then(response => response.json())
        .then(data => {
            cachedDirsContainer.textContent = '';

            if (!data.enabled) {
                const empty = document.createElement('div');
                empty.className = 'cached-dirs-empty';
                empty.textContent = 'Cache not enabled';
                cachedDirsContainer.appendChild(empty);
                return;
            }

            if (!data.directories || data.directories.length === 0) {
                const empty = document.createElement('div');
                empty.className = 'cached-dirs-empty';
                empty.textContent = 'No directories cached yet';
                cachedDirsContainer.appendChild(empty);
                return;
            }

            renderCachedDirectories(data.directories);
        })
        .catch(() => {
            cachedDirsContainer.textContent = '';
            const empty = document.createElement('div');
            empty.className = 'cached-dirs-empty';
            empty.textContent = 'Failed to load directories';
            cachedDirsContainer.appendChild(empty);
        });
}

function renderCachedDirectories(dirs) {
    const list = document.createElement('div');
    list.className = 'cached-dirs-list';

    for (const dir of dirs) {
        const item = document.createElement('div');
        item.className = 'cached-dir-item';

        const path = document.createElement('span');
        path.className = 'cached-dir-path';
        path.textContent = dir.path;

        const count = document.createElement('span');
        count.className = 'cached-dir-count';
        count.textContent = dir.count + ' image' + (dir.count !== 1 ? 's' : '');

        item.appendChild(path);
        item.appendChild(count);
        list.appendChild(item);
    }

    cachedDirsContainer.textContent = '';
    cachedDirsContainer.appendChild(list);
}

refreshDirsBtn.addEventListener('click', loadCachedDirectories);

clearCacheBtn.addEventListener('click', () => {
    if (!confirm('Are you sure you want to clear all cached hashes?')) {
        return;
    }

    clearCacheBtn.disabled = true;
    clearCacheBtn.textContent = 'Clearing...';

    fetch('/api/cache/clear', { method: 'POST' })
        .then(response => response.json())
        .then(data => {
            if (data.success) {
                loadCacheStats();
                loadCachedDirectories();
            } else {
                alert('Failed to clear cache: ' + (data.error || 'Unknown error'));
            }
        })
        .catch(err => {
            alert('Failed to clear cache: ' + err.message);
        })
        .finally(() => {
            clearCacheBtn.disabled = false;
            clearCacheBtn.textContent = 'Clear Cache';
        });
});

scanBrowseBtn.addEventListener('click', () => {
    browserTargetInput = scanDirInput;
    openBrowser(scanDirInput.value || '');
});

let scanEventSource = null;

scanBtn.addEventListener('click', () => {
    const dir = scanDirInput.value.trim();
    if (!dir) {
        alert('Please enter a directory to scan');
        return;
    }

    scanBtn.disabled = true;
    scanProgress.classList.add('active');
    scanProgressBar.style.width = '0%';
    scanProgressText.textContent = 'Starting scan...';

    scanEventSource = new EventSource('/api/cache/scan?dir=' + encodeURIComponent(dir));

    scanEventSource.onmessage = (event) => {
        const data = JSON.parse(event.data);

        if (data.error) {
            scanProgressText.textContent = 'Error: ' + data.error;
            scanEventSource.close();
            scanBtn.disabled = false;
            return;
        }

        if (data.total > 0) {
            const percent = Math.round((data.scanned / data.total) * 100);
            scanProgressBar.style.width = percent + '%';
            scanProgressText.textContent = 'Scanned ' + data.scanned + ' of ' + data.total + ' images (' + data.cached + ' cached)';
        }

        if (data.done) {
            scanEventSource.close();
            scanBtn.disabled = false;
            scanProgressText.textContent = 'Scan complete! ' + data.cached + ' images cached.';
            loadCacheStats();
            loadCachedDirectories();
        }
    };

    scanEventSource.onerror = () => {
        scanEventSource.close();
        scanBtn.disabled = false;
        scanProgressText.textContent = 'Scan error or interrupted';
    };
});
