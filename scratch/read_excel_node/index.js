const XLSX = require('xlsx');
const fs = require('fs');

try {
    const workbook = XLSX.readFile('../../docs/Plans/Association/Data/Association_Industry_FINAL.xlsx');
    const sheetName = workbook.SheetNames[0];
    const worksheet = workbook.Sheets[sheetName];
    
    // Convert worksheet to CSV string
    const csvContent = XLSX.utils.sheet_to_csv(worksheet);
    
    // Write CSV to output file
    fs.writeFileSync('../output.csv', csvContent);
    console.log('Successfully wrote to scratch/output.csv');
} catch (err) {
    console.error('Error reading excel:', err);
}
