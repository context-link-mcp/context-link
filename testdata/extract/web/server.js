const express = require('express');
const app = express();

function listOrders(req, res) {
  res.json([]);
}

app.get('/orders', listOrders);
app.post('/orders/:id/refund', requireAdmin, (req, res) => {
  res.sendStatus(204);
});

function requireAdmin(req, res, next) {
  next();
}
