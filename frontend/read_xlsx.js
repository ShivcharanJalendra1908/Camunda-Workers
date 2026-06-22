const fs = require('fs');
const path = require('path');
const xlsx = require('xlsx');

const dataDir = 'c:\\Users\\lenovo\\Desktop\\LeMiCi\\Camunda-Workers\\docs\\Plans\\Association\\Data';

fs.readdir(dataDir, (err, files) => {
    if (err) {
        console.error('Error reading directory:', err);
        return;
    }

    files.forEach(file => {
        if (!file.endsWith('.xlsx') || file.startsWith('~$')) return;

        const filePath = path.join(dataDir, file);
        console.log('\n========================================================================');
        console.log(`FILE: ${file}`);
        console.log('========================================================================');

        try {
            const workbook = xlsx.readFile(filePath);
            workbook.SheetNames.forEach(sheetName => {
                console.log(`\n--- SHEET: ${sheetName} ---`);
                const sheet = workbook.Sheets[sheetName];
                const rows = xlsx.utils.sheet_to_json(sheet, { header: 1 });
                
                // Print first 15 rows
                rows.slice(0, 15).forEach((row, i) => {
                    console.log(`Row ${i + 1}:`, JSON.stringify(row));
                });

                if (rows.length > 15) {
                    console.log(`... and ${rows.length - 15} more rows`);
                }
            });
        } catch (e) {
            console.error(`Error reading ${file}:`, e.message);
        }
    });
});
