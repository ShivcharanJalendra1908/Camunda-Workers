const fs = require('fs');
const content = fs.readFileSync('internal/workers/infrastructure/build-response/handler_test.go', 'utf8');
const lines = content.split('\n');
lines.forEach((line, idx) => {
    if (line.includes('1949') || line.includes('year_of_establishment')) {
        console.log(`Line ${idx + 1}: ${line.trim()}`);
    }
});
