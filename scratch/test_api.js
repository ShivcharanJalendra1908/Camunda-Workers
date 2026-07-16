const http = require('https');

http.get('https://us-dev-api.lemici.com/api/v1/franchise/search?query=Healthcare%20and%20Technology%20franchises', (res) => {
  let data = '';
  res.on('data', (chunk) => {
    data += chunk;
  });
  res.on('end', () => {
    console.log(data);
  });
}).on("error", (err) => {
  console.log("Error: " + err.message);
});
