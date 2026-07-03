const fs = require('fs');
const content = fs.readFileSync('internal/workers/data-access/query-postgresql/queries/franchise.go', 'utf8');
const lines = content.split('\n');
lines.forEach((line, idx) => {
    if (line.includes('func FranchiseFullDetails') || line.includes('SELECT')) {
        console.log(`Line ${idx + 1}: ${line.trim()}`);
    }
});
