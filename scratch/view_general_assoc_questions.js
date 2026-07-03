const fs = require('fs');

function parseCSV(content) {
    const rows = [];
    let currentRow = [];
    let currentField = '';
    let inQuotes = false;
    for (let i = 0; i < content.length; i++) {
        const char = content[i];
        const nextChar = content[i+1];
        if (char === '"') {
            if (inQuotes && nextChar === '"') {
                currentField += '"';
                i++;
            } else {
                inQuotes = !inQuotes;
            }
        } else if (char === ',' && !inQuotes) {
            currentRow.push(currentField);
            currentField = '';
        } else if ((char === '\r' || char === '\n') && !inQuotes) {
            if (char === '\r' && nextChar === '\n') i++;
            currentRow.push(currentField);
            rows.push(currentRow);
            currentRow = [];
            currentField = '';
        } else {
            currentField += char;
        }
    }
    if (currentRow.length > 0 || currentField) {
        currentRow.push(currentField);
        rows.push(currentRow);
    }
    return rows;
}

const questions = parseCSV(fs.readFileSync('data/postgres/v2-data/category_questions.csv', 'utf8'));
const list = [];
for (let i = 1; i < questions.length; i++) {
    const row = questions[i];
    if (row.length > 2) {
        const refId = row[1];
        const entityType = row[2];
        const question = row[3];
        const intentTag = row[4];
        if (entityType === 'association' && refId === '00000000-0000-0000-0000-000000000000') {
            list.push({ question, intentTag });
        }
    }
}

console.log(JSON.stringify(list, null, 2));
