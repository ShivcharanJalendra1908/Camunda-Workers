const fs = require('fs');
const content = fs.readFileSync('internal/workers/infrastructure/build-response/handler_test.go', 'utf8');
const lines = content.split('\n');
lines.forEach((line, idx) => {
    if (line.includes('functions_of_business_associations') || line.includes('statistics') || line.includes('across_india')) {
        console.log(`Line ${idx + 1}: ${line.trim()}`);
    }
});
