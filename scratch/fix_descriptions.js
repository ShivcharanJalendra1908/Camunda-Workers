const fs = require('fs');

const dataStr = fs.readFileSync('c:\\Users\\lenovo\\Desktop\\LeMiCi\\Camunda-Workers\\scratch\\mismatched_descriptions.json', 'utf8');
const mismatches = JSON.parse(dataStr);

const resolutions = {
    // We will keep the DB description for almost everything as it's much more detailed,
    // EXCEPT for AIOCD and CREDAI where the source description had more accurate member counts.
    "All India Organisation of Chemists & Druggists (AIOCD)": "source",
    "Confederation of Real Estate Developers' Associations of India (CREDAI)": "source"
};

const finalDescriptions = {};
mismatches.forEach(m => {
    if (resolutions[m.name] === "source") {
        finalDescriptions[m.name] = m.source_description;
    } else {
        finalDescriptions[m.name] = m.db_description;
    }
});

// Function to safely encode CSV fields
function encodeCSVField(field) {
    if (field === null || field === undefined) return '';
    let str = String(field);
    if (str.includes(',') || str.includes('"') || str.includes('\n')) {
        str = '"' + str.replace(/"/g, '""') + '"';
    }
    return str;
}

// Function to process a CSV file and update descriptions
function updateCSV(filePath) {
    const content = fs.readFileSync(filePath, 'utf8');
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

    const headers = lines[0].map(h => h.trim());
    const nameIdx = headers.indexOf('name');
    const descIdx = headers.indexOf('description');

    if (nameIdx === -1 || descIdx === -1) {
        console.log(`Could not find name or description in ${filePath}`);
        return;
    }

    let updatedCount = 0;
    const outputLines = [headers.map(encodeCSVField).join(',')];

    for (let i = 1; i < lines.length; i++) {
        if (lines[i].length < 2) continue;
        const name = lines[i][nameIdx].trim();
        const newDesc = finalDescriptions[name];
        
        if (newDesc) {
            lines[i][descIdx] = newDesc;
            updatedCount++;
        }
        
        outputLines.push(lines[i].map(encodeCSVField).join(','));
    }

    fs.writeFileSync(filePath, outputLines.join('\n'));
    console.log(`Updated ${updatedCount} descriptions in ${filePath}`);
}

const file1 = "c:\\Users\\lenovo\\Desktop\\LeMiCi\\Camunda-Workers\\docs\\Plans\\Association\\Data\\Indian_Associations_Final_Mapped.csv";
const file2 = "c:\\Users\\lenovo\\Desktop\\LeMiCi\\Camunda-Workers\\docs\\Plans\\Association\\Data\\Master_Verified_Associations.csv";

updateCSV(file1);
updateCSV(file2);
