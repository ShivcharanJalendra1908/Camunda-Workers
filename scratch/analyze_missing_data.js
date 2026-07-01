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

let stats = {
    total_analyzed: 0,
    missing_president_name: 0,
    missing_email: 0,
    missing_website: 0,
    missing_mission: 0,
    missing_vision: 0
};

for (let i = 1; i < dbData.length; i++) {
    const r = dbData[i];
    if (r.length < 26) continue;
    
    let jStr = r[25];
    if (jStr && jStr.startsWith('"') && jStr.endsWith('"')) jStr = jStr.slice(1, -1);
    if (!jStr) continue;
    jStr = jStr.replace(/""/g, '"');
    
    let jObj = {};
    try { jObj = JSON.parse(jStr); } catch(e) { continue; }
    
    // Check if it's an association profile
    if (jObj.governance || jObj.association_type || jObj.membership_details) {
        stats.total_analyzed++;
        
        const pres = jObj.governance?.president?.name;
        if (!pres || pres.trim() === '') {
            stats.missing_president_name++;
        }
        
        const email = jObj.contact_details?.email;
        if (!email || email.trim() === '') stats.missing_email++;
        
        const web = jObj.contact_details?.website;
        if (!web || web.trim() === '') stats.missing_website++;
        
        const mission = jObj.overview?.mission;
        if (!mission || mission.trim() === '') stats.missing_mission++;
        
        const vision = jObj.overview?.vision;
        if (!vision || vision.trim() === '') stats.missing_vision++;
    }
}

console.log(JSON.stringify(stats, null, 2));
