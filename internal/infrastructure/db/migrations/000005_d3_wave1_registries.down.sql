-- goAthena migration 000005 (down) — remove Wave 1 registry tables.

DROP TABLE IF EXISTS `mapreg`;
DROP TABLE IF EXISTS `global_acc_reg_str`;
DROP TABLE IF EXISTS `global_acc_reg_num`;
DROP TABLE IF EXISTS `char_reg_str`;
DROP TABLE IF EXISTS `char_reg_num`;
DROP TABLE IF EXISTS `acc_reg_str`;
DROP TABLE IF EXISTS `acc_reg_num`;
