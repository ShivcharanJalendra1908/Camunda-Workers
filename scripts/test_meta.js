const fs = require('fs');
function parseCSVLine(text) {
    const result = [];
    let startValueBndry = -1;
    let inQuotes = false;
    for (let i = 0; i < text.length; i++) {
        if (text[i] === '"') {
            inQuotes = !inQuotes;
        } else if (text[i] === ',' && !inQuotes) {
            result.push(text.substring(startValueBndry + 1, i).replace(/^"|"$/g, '').replace(/""/g, '"'));
            startValueBndry = i;
        }
    }
    result.push(text.substring(startValueBndry + 1).replace(/^"|"$/g, '').replace(/""/g, '"'));
    return result;
}
const lines = fs.readFileSync('../data/postgres/new_extracted/franchises.csv', 'utf8').split(/\r?\n/);
const headers = parseCSVLine(lines[0]);
const metadataIdx = headers.indexOf('association_metadata');
const idIdx = headers.indexOf('id');
for (let i = 1; i < lines.length; i++) {
    if (!lines[i].trim()) continue;
    const vals = parseCSVLine(lines[i]);
    if (vals[idIdx] === '4e7e5e16-80a3-4681-a541-ba0bc175895f') {
        console.log('RAW JSON FOR 4e7e5e16:', vals[metadataIdx]);
    }
}
