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

function encodeCSVField(field) {
    if (field === null || field === undefined) return '';
    let str = String(field);
    if (str.includes(',') || str.includes('"') || str.includes('\n') || str.includes('\r')) {
        str = '"' + str.replace(/"/g, '""') + '"';
    }
    return str;
}

const sourceFile1 = "c:\\Users\\lenovo\\Desktop\\LeMiCi\\Camunda-Workers\\docs\\Plans\\Association\\Data\\Indian_Associations_Final_Mapped.csv";
const sourceFile2 = "c:\\Users\\lenovo\\Desktop\\LeMiCi\\Camunda-Workers\\docs\\Plans\\Association\\Data\\Master_Verified_Associations.csv";
const v2DataDir = "c:\\Users\\lenovo\\Desktop\\LeMiCi\\Camunda-Workers\\data\\postgres\\v2-data";

const listingsFile = path.join(v2DataDir, "listings.csv");
const citiesFile = path.join(v2DataDir, "franchise_cities.csv");
const assocFile = path.join(v2DataDir, "associations.csv");

// 1. Build a map of correct source data
const sourceDataMap = {};

function ingestSource(filePath) {
    const lines = parseCSV(fs.readFileSync(filePath, 'utf8'));
    const headers = lines[0].map(h => h.trim());
    lines.slice(1).forEach(l => {
        if (l.length < 2) return;
        const obj = l.reduce((a, v, i) => { if (i < headers.length) a[headers[i]] = v; return a; }, {});
        if (obj.name) {
            sourceDataMap[obj.name.trim()] = obj;
        }
    });
}
ingestSource(sourceFile1);
ingestSource(sourceFile2); // File 2 overwrites File 1 (Master takes precedence)

// 2. Patch listings.csv
let listingsLines = parseCSV(fs.readFileSync(listingsFile, 'utf8'));
const listingsHeaders = listingsLines[0].map(h => h.trim());
const nameIdx = listingsHeaders.indexOf('name');
const webIdx = listingsHeaders.indexOf('website_url');
const emailIdx = listingsHeaders.indexOf('contact_email');
const idIdx = listingsHeaders.indexOf('id');

const listingIdToName = {};

let updatedListings = 0;
for (let i = 1; i < listingsLines.length; i++) {
    if (listingsLines[i].length < 2) continue;
    const name = listingsLines[i][nameIdx].trim();
    const lId = listingsLines[i][idIdx];
    listingIdToName[lId] = name;
    
    const correctData = sourceDataMap[name];
    if (correctData) {
        if (correctData.website !== undefined) {
            listingsLines[i][webIdx] = correctData.website;
        }
        if (correctData.ceo_email !== undefined) {
            listingsLines[i][emailIdx] = correctData.ceo_email;
        }
        updatedListings++;
    }
}
fs.writeFileSync(listingsFile, listingsLines.map(l => l.map(encodeCSVField).join(',')).join('\n'));
console.log(`Updated website and email for ${updatedListings} records in listings.csv`);

// 3. Patch franchise_cities.csv
let citiesLines = parseCSV(fs.readFileSync(citiesFile, 'utf8'));
const citiesHeaders = citiesLines[0].map(h => h.trim());
const clIdIdx = citiesHeaders.indexOf('franchise_id');
const cityIdx = citiesHeaders.indexOf('city');
const stateIdx = citiesHeaders.indexOf('state');

let updatedCities = 0;
for (let i = 1; i < citiesLines.length; i++) {
    if (citiesLines[i].length < 2) continue;
    const lId = citiesLines[i][clIdIdx];
    const name = listingIdToName[lId];
    if (!name) continue;
    
    const correctData = sourceDataMap[name];
    if (correctData) {
        if (correctData.city !== undefined) citiesLines[i][cityIdx] = correctData.city;
        if (correctData.state !== undefined) citiesLines[i][stateIdx] = correctData.state;
        updatedCities++;
    }
}
fs.writeFileSync(citiesFile, citiesLines.map(l => l.map(encodeCSVField).join(',')).join('\n'));
console.log(`Updated city and state for ${updatedCities} records in franchise_cities.csv`);

// 4. Patch associations.csv
let assocLines = parseCSV(fs.readFileSync(assocFile, 'utf8'));
const assocHeaders = assocLines[0].map(h => h.trim());
const alIdIdx = assocHeaders.indexOf('listing_id');
const sectorIdx = assocHeaders.indexOf('sector_represented');

// Add association_metadata column if missing
let metaIdx = assocHeaders.indexOf('association_metadata');
if (metaIdx === -1) {
    assocHeaders.push('association_metadata');
    metaIdx = assocHeaders.length - 1;
    assocLines[0] = assocHeaders;
}

let updatedAssocs = 0;
for (let i = 1; i < assocLines.length; i++) {
    if (assocLines[i].length < 2) continue;
    const lId = assocLines[i][alIdIdx];
    const name = listingIdToName[lId];
    if (!name) continue;
    
    const correctData = sourceDataMap[name];
    if (correctData) {
        if (correctData.sector !== undefined && sectorIdx !== -1) {
            assocLines[i][sectorIdx] = correctData.sector;
        }
        
        // Ensure line is long enough if we added a new column
        while (assocLines[i].length <= metaIdx) {
            assocLines[i].push('');
        }
        
        if (correctData.json_data !== undefined) {
            assocLines[i][metaIdx] = correctData.json_data;
        }
        updatedAssocs++;
    }
}
fs.writeFileSync(assocFile, assocLines.map(l => l.map(encodeCSVField).join(',')).join('\n'));
console.log(`Updated sector and added json_data for ${updatedAssocs} records in associations.csv`);
