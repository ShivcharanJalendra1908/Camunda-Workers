const fs = require('fs');

function parseCSV(text) {
    const lines = []; let currentLine = []; let currentField = ''; let inQuotes = false;
    for (let i = 0; i < text.length; i++) {
        const char = text[i];
        if (char === '"') {
            if (inQuotes && text[i+1] === '"') { currentField += '"'; i++; } else { inQuotes = !inQuotes; }
        } else if (char === ',' && !inQuotes) {
            currentLine.push(currentField); currentField = '';
        } else if (char === '\n' && !inQuotes) {
            currentLine.push(currentField); lines.push(currentLine); currentLine = []; currentField = '';
        } else if (char !== '\r') { currentField += char; }
    }
    if (currentLine.length > 0 || currentField !== '') { currentLine.push(currentField); lines.push(currentLine); }
    return lines;
}

let lines = parseCSV(fs.readFileSync('data/postgres/new_extracted/franchises.csv', 'utf8'));
let createdCount = 0, updatedCount = 0;
for (let i = 1; i < lines.length; i++) {
    if (lines[i][19] === '00000000-0000-0000-0000-000000000001') createdCount++;
    if (lines[i][20] === '00000000-0000-0000-0000-000000000001') updatedCount++;
}
console.log('Total entries:', lines.length - 1);
console.log('created_by matches:', createdCount);
console.log('updated_by matches:', updatedCount);
