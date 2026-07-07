const fs = require('fs');
const path = require('path');

function parseCSV(content) {
    const lines = [];
    let currentLine = [];
    let currentField = '';
    let inQuotes = false;
    for (let i = 0; i < content.length; i++) {
        const char = content[i];
        if (inQuotes) {
            if (char === '"') {
                if (i + 1 < content.length && content[i + 1] === '"') {
                    currentField += '"';
                    i++;
                } else {
                    inQuotes = false;
                }
            } else {
                currentField += char;
            }
        } else {
            if (char === '"') {
                inQuotes = true;
            } else if (char === ',') {
                currentLine.push(currentField);
                currentField = '';
            } else if (char === '\n' || char === '\r') {
                if (char === '\r' && i + 1 < content.length && content[i + 1] === '\n') {
                    i++;
                }
                currentLine.push(currentField);
                lines.push(currentLine);
                currentLine = [];
                currentField = '';
            } else {
                currentField += char;
            }
        }
    }
    if (currentField !== '' || currentLine.length > 0) {
        currentLine.push(currentField);
        lines.push(currentLine);
    }
    return lines;
}

const sourceFile = "c:\\Users\\lenovo\\Desktop\\LeMiCi\\Camunda-Workers\\docs\\Plans\\Association\\Data\\Indian_Associations_Final_Mapped.csv";
const v2DataDir = "c:\\Users\\lenovo\\Desktop\\LeMiCi\\Camunda-Workers\\data\\postgres\\v2-data";
const listingsFile = path.join(v2DataDir, "listings.csv");

const sourceLines = parseCSV(fs.readFileSync(sourceFile, 'utf8'));
const sourceHeaders = sourceLines[0].map(h => h.trim());
const sourceData = sourceLines.slice(1).map(l => l.reduce((a,v,i) => {if(i<sourceHeaders.length) a[sourceHeaders[i]] = v; return a;}, {}));

const listingsLines = parseCSV(fs.readFileSync(listingsFile, 'utf8'));
const listingsHeaders = listingsLines[0].map(h => h.trim());
const listingsData = listingsLines.slice(1).map(l => l.reduce((a,v,i) => {if(i<listingsHeaders.length) a[listingsHeaders[i]] = v; return a;}, {}));

const dbListings = {};
listingsData.forEach(r => { if(r.name) dbListings[r.name.trim()] = r; });

const mismatches = [];
sourceData.forEach(src => {
    if (!src.name) return;
    const dbL = dbListings[src.name.trim()];
    if (!dbL) return;
    
    if (src.description !== dbL.description) {
        mismatches.push({
            name: src.name.trim(),
            website: src.website,
            source_description: src.description,
            db_description: dbL.description
        });
    }
});

fs.writeFileSync("c:\\Users\\lenovo\\Desktop\\LeMiCi\\Camunda-Workers\\scratch\\mismatched_descriptions.json", JSON.stringify(mismatches, null, 2));
console.log(`Saved ${mismatches.length} mismatches to mismatched_descriptions.json`);
