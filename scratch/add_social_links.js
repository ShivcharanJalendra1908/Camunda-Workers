const fs = require('fs');
const crypto = require('crypto');

function uuidv4() {
    return crypto.randomUUID();
}

function parseCSV(text) {
    const lines = [];
    let currentLine = [];
    let currentField = '';
    let inQuotes = false;
    for (let i = 0; i < text.length; i++) {
        const char = text[i];
        if (char === '"') {
            if (inQuotes && text[i+1] === '"') {
                currentField += '"';
                i++;
            } else {
                inQuotes = !inQuotes;
            }
        } else if (char === ',' && !inQuotes) {
            currentLine.push(currentField);
            currentField = '';
        } else if (char === '\n' && !inQuotes) {
            currentLine.push(currentField);
            lines.push(currentLine);
            currentLine = [];
            currentField = '';
        } else if (char !== '\r') {
            currentField += char;
        }
    }
    if (currentLine.length > 0 || currentField !== '') {
        currentLine.push(currentField);
        lines.push(currentLine);
    }
    return lines;
}

function stringifyCSV(rows) {
    return rows.map(row => 
        row.map(field => {
            if (field === null || field === undefined) field = '';
            field = field.toString();
            if (field.includes(',') || field.includes('"') || field.includes('\n')) {
                return '"' + field.replace(/"/g, '""') + '"';
            }
            return field;
        }).join(',')
    ).join('\n');
}

function run() {
    const newFranData = fs.readFileSync('scratch/new_franchises.csv', 'utf-8');
    const newFranchises = parseCSV(newFranData);
    
    const newSocialLinks = [];
    const now = "2026-06-30 00:00:00";

    newFranchises.slice(1).forEach(row => {
        if (row.length < 1) return;
        const fId = row[0];
        
        // id, franchise_id, facebook_url, twitter_url, instagram_url, linkedin_url, youtube_url, created_at, updated_at
        const slId = uuidv4();
        newSocialLinks.push([slId, fId, '', '', '', '', '', now, now]);
    });
    
    if (newSocialLinks.length > 0) {
        fs.appendFileSync('data/postgres/new_extracted/franchise_social_links.csv', '\n' + stringifyCSV(newSocialLinks));
        console.log(`Appended ${newSocialLinks.length} rows to franchise_social_links.csv.`);
    }
}

run();
