const fs = require('fs');
const content = fs.readFileSync('internal/workers/infrastructure/build-response/handler.go', 'utf8');
const lines = content.split('\n');
lines.forEach((line, idx) => {
    if (line.includes('featured_categories') || line.includes('featured_business_categories') || line.includes('explore_by_categories')) {
        console.log(`Line ${idx + 1}: ${line.trim()}`);
    }
});
