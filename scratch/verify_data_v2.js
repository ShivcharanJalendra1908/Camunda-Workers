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
const citiesFile = path.join(v2DataDir, "franchise_cities.csv");
const assocFile = path.join(v2DataDir, "associations.csv");

const sourceLines = parseCSV(fs.readFileSync(sourceFile, 'utf8'));
const sourceHeaders = sourceLines[0].map(h => h.trim());
const sourceData = sourceLines.slice(1).map(l => l.reduce((a,v,i) => {if(i<sourceHeaders.length) a[sourceHeaders[i]] = v; return a;}, {}));

const listingsLines = parseCSV(fs.readFileSync(listingsFile, 'utf8'));
const listingsHeaders = listingsLines[0].map(h => h.trim());
const listingsData = listingsLines.slice(1).map(l => l.reduce((a,v,i) => {if(i<listingsHeaders.length) a[listingsHeaders[i]] = v; return a;}, {}));

const citiesLines = parseCSV(fs.readFileSync(citiesFile, 'utf8'));
const citiesHeaders = citiesLines[0].map(h => h.trim());
const citiesData = citiesLines.slice(1).map(l => l.reduce((a,v,i) => {if(i<citiesHeaders.length) a[citiesHeaders[i]] = v; return a;}, {}));

const assocLines = parseCSV(fs.readFileSync(assocFile, 'utf8'));
const assocHeaders = assocLines[0].map(h => h.trim());
const assocData = assocLines.slice(1).map(l => l.reduce((a,v,i) => {if(i<assocHeaders.length) a[assocHeaders[i]] = v; return a;}, {}));

const dbListings = {};
listingsData.forEach(r => { if(r.name) dbListings[r.name.trim()] = r; });

const dbCities = {};
citiesData.forEach(r => { if(r.franchise_id) dbCities[r.franchise_id] = r; });

const dbAssocs = {};
assocData.forEach(r => { if(r.listing_id) dbAssocs[r.listing_id] = r; });

const issues = {
    short_description: 0,
    description: 0,
    established_year: 0,
    contact_email: 0,
    website: 0,
    entity_type: 0,
    member_count: 0,
    membership_fee_min: 0,
    membership_fee_max: 0,
    city: 0,
    state: 0,
    association_type: 0,
    sector_represented: 0
};

console.log("Analyzing 147 source records against DB seed files...");

sourceData.forEach(src => {
    if (!src.name) return;
    const dbL = dbListings[src.name.trim()];
    if (!dbL) return;
    
    const lId = dbL.id;
    const dbA = dbAssocs[lId] || {};
    const dbC = dbCities[lId] || {};
    
    if (src.short_description !== dbL.short_description) issues.short_description++;
    if (src.description !== dbL.description) issues.description++;
    if (src.established_year !== dbL.founded_year) issues.established_year++;
    if (src.ceo_email !== dbL.contact_email) issues.contact_email++;
    if (src.website !== dbL.website_url) issues.website++;
    if (src.entity_type !== dbL.entity_type) issues.entity_type++;
    
    if (src.member_count !== dbA.member_count) issues.member_count++;
    if (src.min_membership_fee !== dbA.membership_fee_min) issues.membership_fee_min++;
    if (src.max_membership_fee !== dbA.membership_fee_max) issues.membership_fee_max++;
    if (src.association_type !== dbA.association_type) issues.association_type++;
    
    // sector in db is blank for almost all associations according to previous observation, lets check
    if (src.sector !== dbA.sector_represented) issues.sector_represented++;
    
    if (src.city !== dbC.city) issues.city++;
    if (src.state !== dbC.state) issues.state++;
});

console.log("\nTOTAL MISMATCH COUNT FOR EACH FIELD (Out of 147):");
for (const [field, count] of Object.entries(issues)) {
    console.log(`${field.padEnd(20)}: ${count} mismatches`);
}

// Log an example for one of them
const sampleName = "Confederation of Indian Industry (CII)";
const sSrc = sourceData.find(s => s.name === sampleName);
const sDbL = dbListings[sampleName];
const sDbA = dbAssocs[sDbL.id] || {};
const sDbC = dbCities[sDbL.id] || {};

console.log("\nSAMPLE RECORD DIFFERENCES FOR 'CII':");
console.log("Field".padEnd(20) + " | " + "Source".padEnd(40) + " | " + "Database");
console.log("-".repeat(80));
console.log("website".padEnd(20) + " | " + (sSrc.website || '').padEnd(40) + " | " + (sDbL.website_url || ''));
console.log("ceo_email".padEnd(20) + " | " + (sSrc.ceo_email || '').padEnd(40) + " | " + (sDbL.contact_email || ''));
console.log("city".padEnd(20) + " | " + (sSrc.city || '').padEnd(40) + " | " + (sDbC.city || ''));
console.log("state".padEnd(20) + " | " + (sSrc.state || '').padEnd(40) + " | " + (sDbC.state || ''));
console.log("sector_represented".padEnd(20) + " | " + (sSrc.sector || '').padEnd(40) + " | " + (sDbA.sector_represented || ''));
console.log("member_count".padEnd(20) + " | " + (sSrc.member_count || '').padEnd(40) + " | " + (sDbA.member_count || ''));
console.log("min_membership_fee".padEnd(20) + " | " + (sSrc.min_membership_fee || '').padEnd(40) + " | " + (sDbA.membership_fee_min || ''));

