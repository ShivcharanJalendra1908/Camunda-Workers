const fs = require('fs');
const path = require('path');

// Mappings from industry name to UUID
const industryMap = {
    "General": "00000000-0000-0000-0000-000000000000",
    "Automotive": "5ed3c109-2d23-4148-a019-f347fda26ed4",
    "Beauty": "42467fa8-06f1-4047-baf5-212e10ea66d8",
    "Health": "f26791cd-9656-452d-8326-9b9296125906",
    "Business Services": "c39ecac8-9a1f-4ad3-af64-069b9b315926",
    "Dealers & Distributors": "cc637398-2762-4b5b-a999-c1a8c5567cfe",
    "Education": "f5fe36f6-02ef-4ca5-b884-30d22b5402cf",
    "Fashion": "3b1b6be7-d616-4c46-a470-2a2fc1d16691",
    "Food & Beverage": "7b332bd1-117a-4b40-92a1-aa8c5267a02b",
    "Home-Based Business": "1e4a2772-e991-4e90-b77b-2724809f5dbc",
    "Hotel, Travel & Tourism": "d8e835b1-07dd-4279-93b4-5049cfb1c6d6",
    "Retail": "ae11be80-bea3-4d0e-a35a-c964451b1a20",
    "Sports & Fitness": "ead42b5a-26e6-4bd2-a2e3-bb8709ede483",
    "Entertainment": "a28de9c3-9d0d-4058-8782-1211f20ffd1d",
    "Government": "f494351f-bfb3-411d-a178-d1905bd0616e",
    "Real Estate": "1a821422-d8eb-484f-a280-8af8f6b79144",
    "Technology / IT": "64fd1516-f032-4630-8a79-e347851f2e18",
    "Finance / Banking": "4a104cba-ccd3-4f86-b204-2277660117eb",
    "Logistics / Manufacturing": "26d2b88d-ddbf-45b7-a016-9379064270a9",
    "Agriculture": "985ff825-8855-4dec-9615-6d83578ef2cf",
    "Media / Communication": "e42b13ce-89ed-481f-98ab-36ac53927ee4"
};

// Simple CSV parser
function parseCSV(content) {
    const lines = content.split(/\r?\n/);
    if (lines.length === 0 || !lines[0]) return [];
    
    const headers = lines[0].split(',');
    const results = [];
    
    for (let i = 1; i < lines.length; i++) {
        const line = lines[i];
        if (!line.trim()) continue;
        
        // Handle basic commas within quotes
        const row = [];
        let inQuotes = false;
        let current = '';
        for (let j = 0; j < line.length; j++) {
            const char = line[j];
            if (char === '"') {
                inQuotes = !inQuotes;
            } else if (char === ',' && !inQuotes) {
                row.push(current);
                current = '';
            } else {
                current += char;
            }
        }
        row.push(current);
        
        const obj = {};
        headers.forEach((header, index) => {
            obj[header.trim()] = row[index] ? row[index].trim() : '';
        });
        results.push(obj);
    }
    return { headers, rows: results };
}

function run() {
    // 1. Read listings to map name -> id
    const listingsContent = fs.readFileSync('../../data/postgres/v2-data/listings.csv', 'utf-8');
    const { rows: listingRows } = parseCSV(listingsContent);
    const nameToId = {};
    listingRows.forEach(l => {
        // Normalize name: lowercase, trim
        const normName = l.name.toLowerCase().trim();
        nameToId[normName] = l.id;
    });

    // 2. Read output.csv (mappings from excel)
    const outputContent = fs.readFileSync('../output.csv', 'utf-8');
    const { rows: mappingRows } = parseCSV(outputContent);
    const associationIndustryMap = {};
    mappingRows.forEach(r => {
        const assocName = r.association.toLowerCase().trim();
        const industryName = r.industry;
        const industryId = industryMap[industryName];
        if (industryId) {
            associationIndustryMap[assocName] = industryId;
        } else {
            console.warn(`No mapping found for industry name: "${industryName}" of association "${r.association}"`);
        }
    });

    // 3. Match association names to listing IDs
    const idToIndustryId = {};
    let matchedCount = 0;
    for (const assocName in associationIndustryMap) {
        const listingId = nameToId[assocName];
        if (listingId) {
            idToIndustryId[listingId] = associationIndustryMap[assocName];
            matchedCount++;
        } else {
            // Try fuzzy matching or check if any listing name starts with/contains it
            let found = false;
            for (const listName in nameToId) {
                if (listName.includes(assocName) || assocName.includes(listName)) {
                    idToIndustryId[nameToId[listName]] = associationIndustryMap[assocName];
                    matchedCount++;
                    found = true;
                    break;
                }
            }
            if (!found) {
                console.warn(`Could not find listing ID for association name: "${assocName}"`);
            }
        }
    }
    console.log(`Matched ${matchedCount} associations to listing IDs`);

    // 4. Update associations.csv
    const assocCsvContent = fs.readFileSync('../../data/postgres/v2-data/associations.csv', 'utf-8');
    const assocLines = assocCsvContent.split(/\r?\n/);
    const updatedLines = [];
    
    // Header
    const header = assocLines[0];
    updatedLines.push(header);
    
    // Rows
    const sqlUpdates = [];
    for (let i = 1; i < assocLines.length; i++) {
        const line = assocLines[i];
        if (!line.trim()) continue;
        
        const parts = line.split(',');
        const listingId = parts[0].trim();
        let industryId = idToIndustryId[listingId] || '';
        
        // Update 7th column (index 6) or append it
        if (parts.length >= 7) {
            parts[6] = industryId;
        } else {
            while (parts.length < 6) {
                parts.push('');
            }
            parts.push(industryId);
        }
        
        updatedLines.push(parts.join(','));
        
        if (industryId) {
            sqlUpdates.push(`UPDATE associations SET industry_id = '${industryId}' WHERE id = '${listingId}';`);
        }
    }
    
    fs.writeFileSync('../../data/postgres/v2-data/associations.csv', updatedLines.join('\n') + '\n');
    console.log('Successfully updated associations.csv');
    
    // 5. Write SQL updates
    fs.writeFileSync('../update_db.sql', sqlUpdates.join('\n'));
    console.log('Successfully wrote SQL updates to scratch/update_db.sql');
}

run();
