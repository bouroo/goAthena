// Hand-curated test fixture covering every grammar form documented in
// .agents/plans/rathena-compat-roadmap/subplans/n1-packet-db.md:
//
//  1. Bare packet(0xNNNN,LEN) — fixed length.
//  2. packet(0xNNNN,-1) — variable length.
//  3. parseable_packet(0xNNNN,LEN,clif_parse_*,...) — fixed length.
//  4. parseable_packet(0xNNNN,-1,clif_parse_*,...) — variable length.
//  5. #if PACKETVER >= YYYYMMDD ... #endif — version-gated block.
//  6. #if ... #elif ... #endif — elif branch with symbolic entries inside.
//  7. Multi-predicate #if with || — defined(PACKETVER_ZERO) permitted.
//  8. Lines SKIPPED with reason (HEADER_ / sizeof / *Type).
//
// The file is intentionally self-contained; do not include real rAthena
// source here.

#ifndef CLIF_PACKETDB_TEST_HPP
#define CLIF_PACKETDB_TEST_HPP

	#define packet(cmd,length) packetdb_addpacket(cmd,length,nullptr,0)
	#define parseable_packet(cmd,length,func,...) packetdb_addpacket(cmd,length,func,__VA_ARGS__,0)

	packet(0x0064,55);
	packet(0x0065,17);
	packet(0x0069,-1);
	packet(0x006b,-1);
	parseable_packet(0x0072,19,clif_parse_WantToConnection,2,6,10,14,18);
	parseable_packet(0x008c,-1,clif_parse_GlobalMessage,2,4);

	// 8: symbolic entries must be skipped (HEADER_/sizeof/*Type).
	parseable_packet( HEADER_CZ_CONTACTNPC, sizeof( PACKET_CZ_CONTACTNPC ), clif_parse_NpcClicked, 0 );
	packet( inventorylistnormalType, -1 );
	packet( useItemAckType, sizeof( struct PACKET_ZC_USE_ITEM_ACK ) );

// 5: version-gated block (single threshold).
#if PACKETVER >= 20040705
	parseable_packet(0x0072,22,clif_parse_WantToConnection,5,9,13,17,21);
	packet(0x020e,24);
#endif

// 6: #if ... #elif ... #endif (header entries inside, should be skipped
// but the structure must parse and not throw).
#if PACKETVER_MAIN_NUM >= 20120503 || PACKETVER_RE_NUM >= 20120502
	parseable_packet( HEADER_CZ_REQ_RANKING, sizeof( PACKET_CZ_REQ_RANKING ), clif_parse_ranklist, 0 );
#elif PACKETVER >= 20041108
	parseable_packet(0x0072,26,clif_parse_WantToConnection,3,7,11,15,19,23);
	parseable_packet(0x0085,9,clif_parse_WalkToXY,6);
#endif

// 7: multi-predicate with defined(PACKETVER_ZERO).
#if PACKETVER_MAIN_NUM >= 20100817 || PACKETVER_RE_NUM >= 20100706 || defined(PACKETVER_ZERO)
	packet(0x0835,-1);
#endif

#endif /* CLIF_PACKETDB_TEST_HPP */
