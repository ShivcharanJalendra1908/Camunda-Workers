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

let invalidJson = 0;
let validJson = 0;
let emptyJson = 0;
let associationCount = 0;
let missingData = { president: 0, email: 0, website: 0 };

for (let i = 1; i < dbData.length; i++) {
    let r = dbData[i];
    if (r.length >= 26) {
        let jStr = r[25];
        if (!jStr || jStr.trim() === '') {
            emptyJson++;
            continue;
        }
        
        try {
            let jObj = JSON.parse(jStr);
            validJson++;
            
            // It's an association if it has the structure we added (association_type, governance, etc)
            if (jObj.association_type || jObj.governance || jObj.membership_details) {
                associationCount++;
                
                const pres = jObj.governance?.president?.name;
                if (!pres || pres.trim() === '') missingData.president++;
                
                const email = jObj.contact_details?.email;
                if (!email || email.trim() === '') missingData.email++;
                
                const web = jObj.contact_details?.website;
                if (!web || web.trim() === '') missingData.website++;
            }
            
        } catch(e) {
            invalidJson++;
            if (invalidJson <= 3) console.log(`Row ${i} (${r[1]}) Invalid JSON: ${e.message}`);
        }
    }
}

console.log(`JSON Stats -> Valid: ${validJson}, Invalid: ${invalidJson}, Empty: ${emptyJson}`);
console.log(`Total associations identified in DB (excluding standard franchises): ${associationCount}`);
console.log(`Missing Data among Associations:`);
console.log(`- Missing President Name: ${missingData.president}`);
console.log(`- Missing Email: ${missingData.email}`);
console.log(`- Missing Website: ${missingData.website}`);
