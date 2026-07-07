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

const v2DataDir = "c:\\Users\\lenovo\\Desktop\\LeMiCi\\Camunda-Workers\\data\\postgres\\v2-data";
const listingsFile = path.join(v2DataDir, "listings.csv");
const citiesFile = path.join(v2DataDir, "franchise_cities.csv");

const listingsLines = parseCSV(fs.readFileSync(listingsFile, 'utf8'));
const listingIds = new Set(listingsLines.map(l => l[0]));
console.log("Sample Listing IDs:", Array.from(listingIds).slice(1,5));

const citiesLines = parseCSV(fs.readFileSync(citiesFile, 'utf8'));
const franchiseIds = new Set(citiesLines.map(l => l[1]));
console.log("Sample Franchise IDs:", Array.from(franchiseIds).slice(1,5));

let intersection = 0;
franchiseIds.forEach(id => {
    if (listingIds.has(id)) intersection++;
});
console.log("Intersection between listings.id and franchise_cities.franchise_id:", intersection);
