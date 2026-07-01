const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

const idMap = new Map();
function getRealId(id) {
    if (id && (id.startsWith('a0000000-0000-') || id.startsWith('f1000000-0000-'))) {
        if (!idMap.has(id)) {
            idMap.set(id, crypto.randomUUID());
        }
        return idMap.get(id);
    }
    return id;
}

const srcDir = path.join(__dirname, '../data/postgres/new_extracted');
const destDir = path.join(__dirname, '../data/postgres/v2-data');

if (!fs.existsSync(destDir)) {
    fs.mkdirSync(destDir, { recursive: true });
}

function parseCSVLine(text) {
    const result = [];
    let startValueBndry = -1;
    let inQuotes = false;
    for (let i = 0; i < text.length; i++) {
        if (text[i] === '"') {
            inQuotes = !inQuotes;
        } else if (text[i] === ',' && !inQuotes) {
            result.push(text.substring(startValueBndry + 1, i).replace(/^"|"$/g, '').replace(/""/g, '"'));
            startValueBndry = i;
        }
    }
    result.push(text.substring(startValueBndry + 1).replace(/^"|"$/g, '').replace(/""/g, '"'));
    return result;
}

function stringifyCSVLine(arr) {
    return arr.map(field => {
        if (field === null || field === undefined) return '';
        const str = String(field);
        if (str.includes(',') || str.includes('"') || str.includes('\n')) {
            return `"${str.replace(/"/g, '""')}"`;
        }
        return str;
    }).join(',');
}

function parseCSV(filePath) {
    const content = fs.readFileSync(filePath, 'utf-8');
    const lines = content.split(/\r?\n/).filter(line => line.trim() !== '');
    const headers = parseCSVLine(lines[0]);
    
    return lines.slice(1).map(line => {
        const values = parseCSVLine(line);
        const obj = {};
        headers.forEach((h, i) => {
            obj[h] = values[i] !== undefined ? values[i] : null;
        });
        return obj;
    });
}

function writeCSV(filePath, headers, data) {
    const lines = [stringifyCSVLine(headers)];
    for (const row of data) {
        const arr = headers.map(h => row[h]);
        lines.push(stringifyCSVLine(arr));
    }
    fs.writeFileSync(filePath, lines.join('\n') + '\n');
}

// 1. Process franchises.csv -> listings.csv, franchises.csv, associations.csv
console.log('Processing franchises.csv...');
const allListings = parseCSV(path.join(srcDir, 'franchises.csv'));

const listingHeaders = [
    'id', 'name', 'slug', 'short_description', 'description', 'founded_year',
    'contact_email', 'website_url', 'logo_url_circle', 'logo_url_square',
    'created_by', 'updated_by', 'created_at', 'updated_at', 'entity_type', 'status',
    'verified', 'trusted_seller'
];

const franchiseHeaders = [
    'listing_id', 'total_outlets', 'parent_company', 'business_type',
    'established_year', 'units_count', 'leader_name', 'leader_role'
];

const associationHeaders = [
    'listing_id', 'association_type', 'sector_represented', 'member_count',
    'membership_fee_min', 'membership_fee_max'
];

const masterFranchiseHeaders = [
    'listing_id', 'master_franchise_type', 'territory_rights', 'term_length_years'
];

const listingsData = [];
const franchisesData = [];
const associationsData = [];
const masterFranchisesData = [];

for (const row of allListings) {
    const listing = {};
    const realId = getRealId(row.id);
    for (const h of listingHeaders) {
        if (h === 'id') {
            listing[h] = realId;
        } else {
            listing[h] = row[h] !== undefined ? row[h] : null;
        }
    }
    if (!listing.website_url) listing.website_url = null;
    listingsData.push(listing);

    if (row.entity_type === 'franchise') {
        const f = {};
        for (const h of franchiseHeaders) {
            if (h === 'listing_id') {
                f[h] = realId;
            } else {
                f[h] = row[h] !== undefined ? row[h] : null;
            }
        }
        franchisesData.push(f);
    } else if (row.entity_type === 'association') {
        const a = {};
        for (const h of associationHeaders) {
            if (h === 'listing_id') {
                a[h] = realId;
            } else {
                a[h] = row[h] !== undefined ? row[h] : null;
            }
        }
        
        // Parse association_metadata JSONB if present
        if (row.association_metadata && row.association_metadata !== '{}' && row.association_metadata !== '') {
            try {
                const meta = JSON.parse(row.association_metadata);
                if (meta.association_type) a.association_type = meta.association_type;
                if (meta.sector_represented) a.sector_represented = meta.sector_represented;
            } catch(e) {
                console.warn(`Could not parse association_metadata for ID ${row.id}`);
            }
        }
        
        associationsData.push(a);
    } else if (row.entity_type === 'master_franchise') {
        const m = {};
        for (const h of masterFranchiseHeaders) {
            if (h === 'listing_id') {
                m[h] = realId;
            } else {
                m[h] = row[h] !== undefined ? row[h] : null;
            }
        }
        masterFranchisesData.push(m);
    }
}

writeCSV(path.join(destDir, 'listings.csv'), listingHeaders, listingsData);
writeCSV(path.join(destDir, 'franchises.csv'), franchiseHeaders, franchisesData);
writeCSV(path.join(destDir, 'associations.csv'), associationHeaders, associationsData);
writeCSV(path.join(destDir, 'master_franchises.csv'), masterFranchiseHeaders, masterFranchisesData);

// 2. Process Universal Relations
console.log('Processing Universal Relations...');
const copyRelations = [
    { src: 'franchise_stats.csv', dest: 'listing_stats.csv', idCol: 'franchise_id', newIdCol: 'listing_id' },
    { src: 'franchise_categories.csv', dest: 'listing_categories.csv', idCol: 'franchise_id', newIdCol: 'listing_id' },
    { src: 'franchise_social_links.csv', dest: 'listing_social_links.csv', idCol: 'franchise_id', newIdCol: 'listing_id' }
];

for (const rel of copyRelations) {
    if (fs.existsSync(path.join(srcDir, rel.src))) {
        let data = parseCSV(path.join(srcDir, rel.src));
        if (rel.src === 'franchise_social_links.csv') {
            data = data.filter(row => {
                const url = row['instagram_url'];
                return !url || url.startsWith('http') || url.startsWith('https');
            });
        }
        if (data.length > 0) {
            const oldHeaders = Object.keys(data[0]);
            const newHeaders = oldHeaders.map(h => h === rel.idCol ? rel.newIdCol : h);
            
            const newData = data.map(row => {
                const newRow = {};
                for (const h of oldHeaders) {
                    const newH = h === rel.idCol ? rel.newIdCol : h;
                    let val = row[h];
                    if (h === rel.idCol) val = getRealId(val);
                    newRow[newH] = val;
                }
                return newRow;
            });
            writeCSV(path.join(destDir, rel.dest), newHeaders, newData);
        }
    }
}

// 3. Process Franchise Specific Relations (direct copy with ID mapping & filtering)
console.log('Processing Franchise Specific Relations...');
const franchiseIds = new Set(franchisesData.map(f => f.listing_id));
const listingIds = new Set(listingsData.map(l => l.id));

const franchiseSpecificFiles = [
    { name: 'franchise_business_overview.csv', filterKey: 'franchise_id', allowedIds: franchiseIds },
    { name: 'franchise_investment_requirement.csv', filterKey: 'franchise_id', allowedIds: franchiseIds },
    { name: 'franchise_operations.csv', filterKey: 'franchise_id', allowedIds: franchiseIds },
    { name: 'franchise_cities.csv', filterKey: 'franchise_id', allowedIds: listingIds }
];

for (const fileObj of franchiseSpecificFiles) {
    const file = fileObj.name;
    if (fs.existsSync(path.join(srcDir, file))) {
        const data = parseCSV(path.join(srcDir, file));
        if (data.length > 0) {
            const mappedData = data.map(row => {
                const newRow = { ...row };
                if (newRow[fileObj.filterKey]) {
                    newRow[fileObj.filterKey] = getRealId(newRow[fileObj.filterKey]);
                }
                return newRow;
            }).filter(row => {
                const id = row[fileObj.filterKey];
                return fileObj.allowedIds.has(id);
            });
            writeCSV(path.join(destDir, file), Object.keys(data[0]), mappedData);
        }
    }
}

// 4. Other files
console.log('Processing Other Files...');
const otherFiles = [
    'categories.csv',
    'category_questions.csv',
    'industries.csv',
    'industry_market_insights.csv',
    'sub_categories.csv'
];

for (const file of otherFiles) {
    if (fs.existsSync(path.join(srcDir, file))) {
        const data = parseCSV(path.join(srcDir, file));
        if (data.length > 0) {
            writeCSV(path.join(destDir, file), Object.keys(data[0]), data);
        }
    }
}

console.log('Migration complete!');
