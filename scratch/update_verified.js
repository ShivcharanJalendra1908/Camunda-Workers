const fs = require('fs');
const { parse } = require('csv-parse/sync');
const { stringify } = require('csv-stringify/sync');

const filePath = 'data/postgres/v2-data/listings.csv';
const content = fs.readFileSync(filePath, 'utf8');

const records = parse(content, {
    columns: true,
    skip_empty_lines: true
});

let updatedCount = 0;
for (const record of records) {
    if (record.verified === 'true' || record.verified === 't' || record.verified === '1') {
        record.verified = 'false';
        updatedCount++;
    } else if (record.verified === '') {
        record.verified = 'false';
        updatedCount++;
    }
}

const output = stringify(records, { header: true });
fs.writeFileSync(filePath, output);
console.log(`Updated ${updatedCount} records to verified = false in listings.csv`);
