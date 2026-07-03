const fs = require('fs');
const content = fs.readFileSync('internal/workers/data-access/franchise-postgres/handler_test.go', 'utf8');
const lines = content.split('\n');
lines.forEach((line, idx) => {
    if (line.includes('expectedFranchiseQuery') || line.includes('franchiseQuery :=')) {
        console.log(`Line ${idx + 1}: ${line.trim()}`);
    }
});
