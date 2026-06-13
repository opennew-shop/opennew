/**
 * SUB-029: Custodial Escrow Handlers
 *
 * Provides 6 API route handlers for custodial escrow management:
 *   POST /api/v1/escrow/lock
 *   POST /api/v1/escrow/confirm-delivery
 *   POST /api/v1/escrow/confirm-receipt
 *   POST /api/v1/escrow/release
 *   GET  /api/v1/escrow/status
 *   GET  /api/v1/escrow/history
 *
 * Include in test-mock-server.cjs after the in-memory stores are defined.
 * The module expects these to be available in the calling scope:
 *   ESCROW_ACCOUNTS, ESCROW_TTL_MS
 *   AGENT_TOKENS, AGENT_WALLETS
 *   jsonResponse(), generateId(), autoReleaseEscrow(), tryReleaseEscrow()
 */

module.exports = function registerEscrowRoutes(serverContext) {
  const {
    jsonResponse, generateId,
    autoReleaseEscrow, tryReleaseEscrow,
    ESCROW_ACCOUNTS, ESCROW_TTL_MS,
    AGENT_TOKENS, AGENT_WALLETS
  } = serverContext;

  return {
    /**
     * POST /api/v1/escrow/lock — Lock buyer funds in custodial escrow
     */
    handleEscrowLock: function(req, res, parsedUrl, path) {
      let body = '';
      req.on('data', chunk => body += chunk);
      req.on('end', () => {
        try {
          const data = JSON.parse(body);
          if (!data.order_id) return jsonResponse(res, 400, { code: 400, message: 'order_id is required' });
          if (!data.buyer_wallet) return jsonResponse(res, 400, { code: 400, message: 'buyer_wallet is required' });
          if (!data.seller_wallet) return jsonResponse(res, 400, { code: 400, message: 'seller_wallet is required' });
          if (!data.amount_agp_minor) return jsonResponse(res, 400, { code: 400, message: 'amount_agp_minor is required' });

          if (ESCROW_ACCOUNTS[data.order_id]) {
            return jsonResponse(res, 409, { code: 409, message: 'Escrow already exists for order ' + data.order_id.slice(0, 20) + '...' });
          }

          const now = new Date().toISOString();
          const escrow = {
            order_id: data.order_id,
            intent_id: data.intent_id || null,
            quote_id: data.quote_id || null,
            buyer_wallet: data.buyer_wallet,
            seller_wallet: data.seller_wallet,
            amount_agp_minor: data.amount_agp_minor,
            currency: data.currency || 'AGP',
            status: 'locked',
            locked_at: now,
            delivery_confirmed_at: null,
            receipt_confirmed_at: null,
            released_at: null,
            delivery_confirmed: false,
            receipt_confirmed: false,
            delivery_proof: null,
            receipt_proof: null
          };
          ESCROW_ACCOUNTS[data.order_id] = escrow;
          console.log('[Escrow] Manual lock: order=' + data.order_id.slice(0, 20) + '... buyer=' + data.buyer_wallet.slice(0, 12) + '... seller=' + data.seller_wallet.slice(0, 12) + '... amount=' + data.amount_agp_minor + ' AGP');
          return jsonResponse(res, 201, escrow);
        } catch(e) {
          return jsonResponse(res, 400, { code: 400, message: 'Invalid escrow lock request: ' + e.message });
        }
      });
    },

    /**
     * POST /api/v1/escrow/confirm-delivery — Seller confirms delivery
     */
    handleEscrowConfirmDelivery: function(req, res, parsedUrl, path) {
      let body = '';
      req.on('data', chunk => body += chunk);
      req.on('end', () => {
        try {
          const data = JSON.parse(body);
          if (!data.order_id) return jsonResponse(res, 400, { code: 400, message: 'order_id is required' });

          const escrow = ESCROW_ACCOUNTS[data.order_id];
          if (!escrow) return jsonResponse(res, 404, { code: 404, message: 'Escrow not found for order ' + data.order_id.slice(0, 20) + '...' });
          if (escrow.status === 'released') return jsonResponse(res, 409, { code: 409, message: 'Escrow already released' });
          if (escrow.delivery_confirmed) return jsonResponse(res, 409, { code: 409, message: 'Delivery already confirmed' });

          escrow.delivery_confirmed = true;
          escrow.delivery_confirmed_at = new Date().toISOString();
          escrow.delivery_proof = data.delivery_proof || null;
          escrow.status = 'delivery_confirmed';

          console.log('[Escrow] Delivery confirmed: order=' + data.order_id.slice(0, 20) + '... by seller=' + escrow.seller_wallet.slice(0, 12) + '...');

          var released = autoReleaseEscrow(data.order_id);
          if (released) {
            return jsonResponse(res, 200, {
              order_id: data.order_id,
              status: 'released',
              message: 'Delivery confirmed. Escrow auto-released to seller.',
              escrow: released
            });
          }

          return jsonResponse(res, 200, {
            order_id: data.order_id,
            status: 'delivery_confirmed',
            message: 'Delivery confirmed. Awaiting buyer receipt confirmation (or 72h TTL).',
            escrow: escrow
          });
        } catch(e) {
          return jsonResponse(res, 400, { code: 400, message: 'Invalid confirm-delivery request: ' + e.message });
        }
      });
    },

    /**
     * POST /api/v1/escrow/confirm-receipt — Buyer confirms receipt
     */
    handleEscrowConfirmReceipt: function(req, res, parsedUrl, path) {
      let body = '';
      req.on('data', chunk => body += chunk);
      req.on('end', () => {
        try {
          const data = JSON.parse(body);
          if (!data.order_id) return jsonResponse(res, 400, { code: 400, message: 'order_id is required' });

          const escrow = ESCROW_ACCOUNTS[data.order_id];
          if (!escrow) return jsonResponse(res, 404, { code: 404, message: 'Escrow not found for order ' + data.order_id.slice(0, 20) + '...' });
          if (escrow.status === 'released') return jsonResponse(res, 409, { code: 409, message: 'Escrow already released' });
          if (escrow.receipt_confirmed) return jsonResponse(res, 409, { code: 409, message: 'Receipt already confirmed' });

          escrow.receipt_confirmed = true;
          escrow.receipt_confirmed_at = new Date().toISOString();
          escrow.receipt_proof = data.receipt_proof || null;
          escrow.status = 'receipt_confirmed';

          console.log('[Escrow] Receipt confirmed: order=' + data.order_id.slice(0, 20) + '... by buyer=' + escrow.buyer_wallet.slice(0, 12) + '...');

          var released2 = autoReleaseEscrow(data.order_id);
          if (released2) {
            return jsonResponse(res, 200, {
              order_id: data.order_id,
              status: 'released',
              message: 'Receipt confirmed. Escrow auto-released to seller.',
              escrow: released2
            });
          }

          return jsonResponse(res, 200, {
            order_id: data.order_id,
            status: 'receipt_confirmed',
            message: 'Receipt confirmed. Awaiting seller delivery confirmation for release.',
            escrow: escrow
          });
        } catch(e) {
          return jsonResponse(res, 400, { code: 400, message: 'Invalid confirm-receipt request: ' + e.message });
        }
      });
    },

    /**
     * POST /api/v1/escrow/release — Manually trigger escrow release
     */
    handleEscrowRelease: function(req, res, parsedUrl, path) {
      let body = '';
      req.on('data', chunk => body += chunk);
      req.on('end', () => {
        try {
          const data = JSON.parse(body);
          if (!data.order_id) return jsonResponse(res, 400, { code: 400, message: 'order_id is required' });

          const result = tryReleaseEscrow(data.order_id);
          if (result.released) {
            return jsonResponse(res, 200, {
              order_id: data.order_id,
              status: 'released',
              message: 'Escrow released to seller.',
              escrow: result.escrow
            });
          }
          return jsonResponse(res, 409, {
            code: 409,
            message: 'Cannot release escrow: ' + result.reason,
            escrow: result.escrow
          });
        } catch(e) {
          return jsonResponse(res, 400, { code: 400, message: 'Invalid escrow release request: ' + e.message });
        }
      });
    },

    /**
     * GET /api/v1/escrow/status — Query escrow status by order_id
     */
    handleEscrowStatus: function(req, res, parsedUrl, path) {
      const orderId = parsedUrl.searchParams.get('order_id');
      if (!orderId) return jsonResponse(res, 400, { code: 400, message: 'order_id query parameter is required' });

      const escrow = ESCROW_ACCOUNTS[orderId];
      if (!escrow) return jsonResponse(res, 404, { code: 404, message: 'Escrow not found for order ' + orderId.slice(0, 40) });

      const elapsed = Date.now() - new Date(escrow.locked_at).getTime();
      const ttlRemainingMs = ESCROW_TTL_MS - elapsed;
      const ttlExpired = ttlRemainingMs <= 0;

      return jsonResponse(res, 200, Object.assign({}, escrow, {
        ttl_remaining_hours: Math.max(0, Math.round(ttlRemainingMs / 3600000 * 10) / 10),
        ttl_expired: ttlExpired,
        can_auto_release: escrow.delivery_confirmed && (escrow.receipt_confirmed || ttlExpired) && escrow.status !== 'released'
      }));
    },

    /**
     * GET /api/v1/escrow/history — View escrow history by wallet or agent token
     */
    handleEscrowHistory: function(req, res, parsedUrl, path) {
      const wallet = parsedUrl.searchParams.get('wallet');
      const agentToken = parsedUrl.searchParams.get('agent_token');

      var escrows = Object.values(ESCROW_ACCOUNTS);

      if (wallet) {
        escrows = escrows.filter(function(e) { return e.buyer_wallet === wallet || e.seller_wallet === wallet; });
      } else if (agentToken) {
        const entry = AGENT_TOKENS[agentToken];
        if (entry) {
          const boundWallets = (AGENT_WALLETS[entry.agent_id] || []).map(function(w) { return w.address; });
          escrows = escrows.filter(function(e) { return boundWallets.includes(e.buyer_wallet) || boundWallets.includes(e.seller_wallet); });
        }
      }

      escrows.sort(function(a, b) { return new Date(b.locked_at) - new Date(a.locked_at); });

      var limit = parseInt(parsedUrl.searchParams.get('limit') || '50', 10);
      var offset = parseInt(parsedUrl.searchParams.get('offset') || '0', 10);
      var paged = escrows.slice(offset, offset + limit);

      var totalLocked = BigInt(0);
      var totalReleased = BigInt(0);
      var lockedCount = 0;
      var releasedCount = 0;
      for (var i = 0; i < escrows.length; i++) {
        var e = escrows[i];
        var amt = BigInt(e.amount_agp_minor);
        if (e.status === 'released') {
          totalReleased = totalReleased + amt;
          releasedCount++;
        } else {
          totalLocked = totalLocked + amt;
          lockedCount++;
        }
      }

      return jsonResponse(res, 200, {
        items: paged,
        total: escrows.length,
        limit: limit,
        offset: offset,
        summary: {
          total_locked_agp_minor: totalLocked.toString(),
          total_released_agp_minor: totalReleased.toString(),
          locked_count: lockedCount,
          released_count: releasedCount
        }
      });
    }
  };
};
