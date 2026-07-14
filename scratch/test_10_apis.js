const fs = require('fs');

async function testAPIs() {
  const queries = [
    "IMA or FICCI for doctors and hospitals",
    "Healthcare and Technology associations in Delhi NCR with more than 500 members",
    "Education society",
    "IDA",
    "Real estate associations in Mumbai under 100000 fees",
    "Top AI consortiums",
    "Food and Beverage guild",
    "Export associations with 5000 members",
    "Retail forum in Maharashtra",
    "Automobile federation with 10L membership fee"
  ];

  for (let i = 0; i < queries.length; i++) {
    const query = queries[i];
    const encoded = encodeURIComponent(query);
    const url = `https://us-dev-api.lemici.com/api/v1/association/search?query=${encoded}`;
    
    try {
      const response = await fetch(url);
      const data = await response.json();
      
      const total = data.data?.pagination?.totalItems || 0;
      
      console.log(`\n--- Test ${i + 1} ---`);
      console.log(`Query: ${query}`);
      console.log(`Total Results: ${total}`);
      
      if (total > 0 && data.data?.sections) {
        // Find the section that contains the list
        let items = [];
        for (const section of data.data.sections) {
          if (Array.isArray(section.data) && section.data.length > 0) {
            items = section.data;
            break;
          }
        }
        
        console.log("Top 2 results:");
        for (let j = 0; j < Math.min(2, items.length); j++) {
          const item = items[j];
          console.log(`  - ${item.association_name || item.name} (Industry: ${item.industry || item.industry_name}, Location: ${item.location?.city || item.City || item.city})`);
        }
      }
    } catch (e) {
      console.error(`Error fetching ${query}:`, e.message);
    }
  }
}

testAPIs();
