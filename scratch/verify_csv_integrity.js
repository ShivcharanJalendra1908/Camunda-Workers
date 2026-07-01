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

const dbData = parseCSV(fs.readFileSync('data/postgres/new_extracted/franchises.csv', 'utf8'));

let invalidRows = 0;
let expectedCols = dbData[0].length;

for (let i = 0; i < dbData.length; i++) {
    if (dbData[i].length !== expectedCols && dbData[i].length > 1) { // ignore empty trailing newline
        console.log(`Row ${i} has ${dbData[i].length} cols instead of ${expectedCols}. Name: ${dbData[i][1]}`);
        invalidRows++;
    }
}

if (invalidRows === 0) {
    console.log(`All ${dbData.length} rows have exactly ${expectedCols} columns. CSV is structurally sound.`);
} else {
    console.log(`Found ${invalidRows} structurally broken rows!`);
}

// Check JSON validity in column 25
let invalidJson = 0;
let validJson = 0;
let emptyJson = 0;

for (let i = 1; i < dbData.length; i++) {
    let r = dbData[i];
    if (r.length === expectedCols) {
        let jStr = r[25];
        if (!jStr || jStr.trim() === '') {
            emptyJson++;
            continue;
        }
        if (jStr.startsWith('"') && jStr.endsWith('"')) {
            jStr = jStr.slice(1, -1);
        }
        jStr = jStr.replace(/""/g, '"');
        try {
            JSON.parse(jStr);
            validJson++;
        } catch(e) {
            invalidJson++;
            console.log(`Row ${i} (${r[1]}) has invalid JSON! Error: ${e.message}`);
        }
    }
}

console.log(`JSON Stats -> Valid: ${validJson}, Invalid: ${invalidJson}, Empty: ${emptyJson}`);
