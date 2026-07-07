const fs = require('fs');
const path = require('path');
const readline = require('readline');

// I will just use simple string splitting for CSV parsing, or require a fast parser.
// Actually, string split by comma is dangerous if there are quotes.
// Since we are running in an environment where we can write to a file and execute it, let's just write a robust CSV parser.
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
const citiesFile = path.join(v2DataDir, "franchise_cities.csv");
const assocFile = path.join(v2DataDir, "associations.csv");

const sourceContent = fs.readFileSync(sourceFile, 'utf8');
const sourceLines = parseCSV(sourceContent);
const sourceHeaders = sourceLines[0].map(h => h.trim());
const sourceData = sourceLines.slice(1).filter(l => l.length > 1).map(line => {
    const obj = {};
    line.forEach((val, i) => {
        if(i < sourceHeaders.length) obj[sourceHeaders[i]] = val;
    });
    return obj;
});

const listingsContent = fs.readFileSync(listingsFile, 'utf8');
const listingsLines = parseCSV(listingsContent);
const listingsHeaders = listingsLines[0].map(h => h.trim());
const dbListings = {};
listingsLines.slice(1).filter(l => l.length > 1).forEach(line => {
    const obj = {};
    line.forEach((val, i) => {
        if(i < listingsHeaders.length) obj[listingsHeaders[i]] = val;
    });
    if (obj.name) dbListings[obj.name.trim()] = obj;
});

const citiesContent = fs.readFileSync(citiesFile, 'utf8');
const citiesLines = parseCSV(citiesContent);
const dbCities = {};
citiesLines.slice(1).filter(l => l.length > 2).forEach(line => {
    dbCities[line[1]] = line[2];
});

const assocContent = fs.readFileSync(assocFile, 'utf8');
const assocLines = parseCSV(assocContent);
const assocHeaders = assocLines[0].map(h => h.trim());
const dbAssocs = {};
assocLines.slice(1).filter(l => l.length > 1).forEach(line => {
    const obj = {};
    line.forEach((val, i) => {
        if(i < assocHeaders.length) obj[assocHeaders[i]] = val;
    });
    dbAssocs[obj.listing_id] = obj;
});

const mismatches = [];
const missingInDb = [];

for (const row of sourceData) {
    const name = (row.name || '').trim();
    if (!name) continue;
    if (!dbListings[name]) {
        missingInDb.push(name);
        continue;
    }
    
    const dbListing = dbListings[name];
    const lId = dbListing.id;
    
    const srcWeb = (row.website || '').trim();
    const dbWeb = (dbListing.website_url || '').trim();
    if (srcWeb !== dbWeb) {
        mismatches.push(`[${name}] Website mismatch: Source='${srcWeb}', DB='${dbWeb}'`);
    }
    
    const srcCity = (row.city || '').trim();
    const dbCity = (dbCities[lId] || '').trim();
    if (srcCity !== dbCity) {
        mismatches.push(`[${name}] Location mismatch: Source='${srcCity}', DB='${dbCity}'`);
    }
    
    if (!dbAssocs[lId]) {
        mismatches.push(`[${name}] Missing from associations.csv entirely!`);
    }
    
    // CEO Name is not in DB listings or associations. Let's see if we can find it anywhere.
    // The user's metadata was json_data in source. In DB, we don't have association_metadata seeded!
}

console.log("=== MISSING IN DB ===");
console.log(`Total: ${missingInDb.length}`);
if (missingInDb.length > 0) {
    console.log(missingInDb.slice(0, 5), "...");
}

console.log("\\n=== MISMATCHES ===");
console.log(`Total: ${mismatches.length}`);
for (const m of mismatches.slice(0, 50)) {
    console.log(m);
}
